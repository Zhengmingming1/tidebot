package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/policy"
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
	ctx := parent
	if d.TimeoutMinutes > 0 { // 0 = unlimited; never an instant deadline
		var timeout context.CancelFunc
		ctx, timeout = context.WithTimeout(parent, time.Duration(d.TimeoutMinutes)*time.Minute)
		defer timeout()
	}
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
	parallel := d.MaxParallel
	if d.Writes() { // one shared writable folder: one change at a time
		parallel = 1
	}
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
				if !ready || active >= parallel {
					continue
				}
				if n.Kind == "skill" && d.Budget > 0 && s.OverBudget != nil {
					if why := s.OverBudget(ctx, rid, d); why != "" {
						// Nodes already going finish (each started capped at what was left).
						failure = fmt.Errorf("%w: %s", ErrBudget, why)
						stopped = true
						break
					}
				}
				template := n.Prompt
				if n.Kind == "condition" {
					template = n.Condition.Source
				}
				var prompt string
				var err error
				if n.Kind == "skill" && n.Context != nil {
					n, prompt, err = prepareContext(n, rid, input, states)
				} else {
					prompt, err = render(template, input, states, n.Join)
				}
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
	var output string
	var err error
	for attempt := 0; ; attempt++ {
		if err = ctx.Err(); err != nil {
			break
		}
		var persistentFailure bool
		output, err, persistentFailure = s.nodeAttempt(ctx, rid, n, &state, prompt, provider)
		if err == nil || persistentFailure || ctx.Err() != nil || attempt >= n.RetryMax {
			break
		}
		if _, retryable := policy.Classify(err.Error()); !retryable || errors.Is(err, context.Canceled) || errors.Is(err, errExecutorPanic) {
			break
		}
		s.retries.Add(1)
		base := n.RetryBackoffMS
		if base == 0 {
			base = 1000
		}
		delay := min(time.Duration(base)*time.Millisecond<<attempt, time.Minute)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
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

// nodeAttempt scopes child callbacks to one attempt. Late callbacks cannot
// overwrite another attempt's link or race the final result.
func (s *Service) nodeAttempt(ctx context.Context, rid string, n Node, state *NodeRun, prompt, provider string) (string, error, bool) {
	nodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var linkErr error
	closed := false
	output, err := invokeExecutor(s.Execute, nodeCtx, n, prompt, provider, func(child string) {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		state.ChildRunID = child
		if e := s.putNode(rid, *state); e != nil {
			linkErr = e
			cancel()
		}
	})
	mu.Lock()
	defer mu.Unlock()
	closed = true
	if linkErr != nil {
		return "", fmt.Errorf("cannot persist child run: %w", linkErr), true
	}
	return output, err, false
}

var errExecutorPanic = errors.New("workflow executor panicked")

func invokeExecutor(exec Executor, ctx context.Context, n Node, prompt, provider string, child func(string)) (output string, err error) {
	defer func() {
		if recover() != nil {
			output, err = "", errExecutorPanic
		}
	}()
	return exec(ctx, n, prompt, provider, child)
}
