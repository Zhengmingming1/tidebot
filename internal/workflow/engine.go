package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// readiness waits for every predecessor even for any-joins. This makes a merge
// of mutually exclusive branches deterministic and does not race parallel results.
func readiness(n Node, states map[string]*NodeRun) (ready, skip bool) {
	if len(n.Needs) == 0 {
		return true, false
	}
	active := 0
	for _, d := range n.Needs {
		p := states[d.NodeID]
		if p.Status == "pending" || p.Status == "running" || p.Status == "queued" {
			return false, false
		}
		if p.Status == "succeeded" && (d.Branch == "" || d.Branch == p.Branch) {
			active++
		}
	}
	if n.Join == "any" {
		return active > 0, active == 0
	}
	return active == len(n.Needs), active != len(n.Needs)
}
func (s *Service) run(parent context.Context, rid string, d Definition, input, provider string) {
	ctx, timeout := context.WithTimeout(parent, time.Duration(d.TimeoutMinutes)*time.Minute)
	defer timeout()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if err := s.putRun(rid, "running", "", true); err != nil {
		s.report(err)
		s.report(s.putRun(rid, "failed", "cannot persist workflow start", false))
		return
	}
	states := map[string]*NodeRun{}
	for _, n := range d.Nodes {
		states[n.ID] = &NodeRun{NodeID: n.ID, Status: "pending"}
	}
	results := make(chan NodeRun, len(d.Nodes))
	active := 0
	var failure error
	stopped := false
	for {
		if ctx.Err() != nil {
			stopped = true
		}
		progressed := false
		if !stopped {
			for _, n := range d.Nodes {
				state := states[n.ID]
				if state.Status != "pending" {
					continue
				}
				ready, skip := readiness(n, states)
				if skip {
					state.Status = "skipped"
					state.Error = "dependency or branch not selected"
					state.FinishedAt = now()
					if err := s.putNode(rid, *state); err != nil {
						failure = err
						cancel(err)
						stopped = true
						break
					}
					progressed = true
					continue
				}
				if !ready || active >= d.MaxParallel {
					continue
				}
				template := n.Prompt
				if n.Kind == "condition" {
					template = n.Condition.Source
				}
				prompt, err := render(template, input, states, n.Join)
				state.StartedAt = now()
				if err != nil {
					state.Status = "failed"
					state.Error = err.Error()
					state.FinishedAt = now()
					s.report(s.putNode(rid, *state))
					failure = err
					cancel(err)
					stopped = true
					break
				}
				if n.Kind == "condition" {
					state.Status = "succeeded"
					state.Branch = strconv.FormatBool(testCondition(*n.Condition, prompt))
					state.Output = state.Branch
					state.FinishedAt = now()
					if err := s.putNode(rid, *state); err != nil {
						failure = err
						cancel(err)
						stopped = true
						break
					}
					progressed = true
					continue
				}
				state.Status = "running"
				if err := s.putNode(rid, *state); err != nil {
					failure = err
					cancel(err)
					stopped = true
					break
				}
				active++
				progressed = true
				go s.executeNode(ctx, rid, n, *state, prompt, provider, results)
			}
		}
		if stopped {
			for _, n := range states {
				if n.Status == "pending" {
					n.Status = "canceled"
					n.Error = "workflow stopped before this node started"
					n.FinishedAt = now()
					s.report(s.putNode(rid, *n))
				}
			}
		}
		if active == 0 {
			pending := false
			for _, n := range states {
				if n.Status == "pending" {
					pending = true
				}
			}
			if !pending {
				break
			}
			if progressed {
				continue
			}
			failure = errors.New("workflow has unresolved dependencies")
			cancel(failure)
			stopped = true
			continue
		}
		if progressed && !stopped {
			continue
		}
		// Once canceled, drain results without selecting the permanently-ready Done
		// channel. Child runs must finish recording their own costs before the parent ends.
		var result NodeRun
		if stopped {
			result = <-results
		} else {
			select {
			case result = <-results:
			case <-ctx.Done():
				continue
			}
		}
		active--
		*states[result.NodeID] = result
		if err := s.putNode(rid, result); err != nil && failure == nil {
			failure = err
			cancel(err)
			stopped = true
		}
		if result.Status == "failed" && failure == nil {
			failure = fmt.Errorf("node %s: %s", result.NodeID, result.Error)
			cancel(failure)
			stopped = true
		}
	}
	status, message := "succeeded", ""
	switch {
	case errors.Is(context.Cause(parent), ErrInterrupted):
		status, message = "interrupted", ErrInterrupted.Error()
	case errors.Is(context.Cause(parent), ErrCanceled):
		status, message = "canceled", ErrCanceled.Error()
	case failure != nil:
		status, message = "failed", failure.Error()
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, message = "failed", "workflow timeout"
	case ctx.Err() != nil:
		status, message = "canceled", context.Cause(ctx).Error()
	}
	s.report(s.putRun(rid, status, message, false))
}
func (s *Service) executeNode(ctx context.Context, rid string, n Node, state NodeRun, prompt, provider string, out chan<- NodeRun) {
	nodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var linkErr error
	output, err := s.Execute(nodeCtx, n, prompt, provider, func(child string) {
		mu.Lock()
		defer mu.Unlock()
		state.ChildRunID = child
		if e := s.putNode(rid, state); e != nil {
			linkErr = e
			cancel()
		}
	})
	mu.Lock()
	defer mu.Unlock()
	if linkErr != nil {
		err = fmt.Errorf("cannot persist child run: %w", linkErr)
	}
	state.FinishedAt = now()
	if err == nil && len(output) > MaxOutputBytes {
		err = fmt.Errorf("node output exceeds %d KB; summarize it before passing downstream", MaxOutputBytes>>10)
	}
	if err != nil {
		state.Status = "failed"
		state.Error = err.Error()
		if ctx.Err() != nil {
			state.Status = "canceled"
		}
	} else if ctx.Err() != nil {
		state.Status = "canceled"
		state.Error = context.Cause(ctx).Error()
	} else {
		state.Status = "succeeded"
		state.Output = output
	}
	out <- state
}
