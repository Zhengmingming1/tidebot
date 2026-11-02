package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func (a *App) initWorkflows() {
	a.Workflows = workflow.New(a.DB, a.executeWorkflowNode)
	a.Workflows.Prepare = a.prepareWorkflow
	a.Workflows.OnError = func(err error) { a.Log.Error("workflow persistence", "error", err) }
}
func (a *App) prepareWorkflow(ctx context.Context, d workflow.Definition) (workflow.Definition, error) {
	// Resolve once per binding for the whole graph, before any model call starts.
	d.Nodes = append([]workflow.Node(nil), d.Nodes...)
	resolved := map[string]tasksvc.SkillBinding{}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.Kind != "skill" {
			continue
		}
		if err := a.Skills.ValidateSetSource(ctx, n.SkillSetSource); err != nil {
			return d, fmt.Errorf("node %s: %w", n.ID, err)
		}
		n.SkillSetState = ""
		if n.SkillSetSource != nil {
			n.SkillSetState = a.Skills.SourceState(ctx, n.SkillSetSource, n.SkillBindings)
		}
		bs, err := tasksvc.NormalizeBindings(n.SkillBindings)
		if err != nil {
			return d, err
		}
		for j, b := range bs {
			key := b.SkillID + "\x00" + b.Mode + "\x00" + b.VersionID
			frozen, ok := resolved[key]
			if !ok {
				one, err := a.Skills.ResolveBindings(ctx, []tasksvc.SkillBinding{{SkillID: b.SkillID, Mode: b.Mode, VersionID: b.VersionID}})
				if err != nil {
					return d, fmt.Errorf("node %s: %w", n.ID, err)
				}
				frozen = one[0]
				resolved[key] = frozen
			}
			frozen.Role = b.Role
			bs[j] = frozen
		}
		n.SkillBindings = bs
	}
	return d, nil
}
func (a *App) executeWorkflowNode(ctx context.Context, n workflow.Node, prompt, provider string, onChild func(string)) (string, error) {
	st, err := a.stageSkills(ctx, n.SkillBindings)
	if err != nil {
		return "", err
	}
	defer st.Close()
	if st == nil {
		return "", fmt.Errorf("node %s has no skills", n.ID)
	}
	ws := filepath.Join(st.Dir, "work")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		return "", err
	}
	modern := a.modernClaude()
	args, err := st.CheckedArgs(modern, true)
	if err != nil {
		return "", err
	}
	args = append(args, "--disallowedTools", strings.Join(readOnlyDeny, ","))
	contextRequest, err := workflowContext(st, n, modern, ws)
	if err != nil {
		return "", err
	}
	primary := n.SkillBindings[0]
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var recordErr error
	res, err := a.RunPromptEx(childCtx, runner.Options{
		Prompt: st.Invoke(modern, prompt), PromptStdin: true, DisplayPrompt: prompt,
		WorkspaceDir: ws, TriggerKind: "workflow", CostBucket: cost.BucketProduction,
		ContextArgs: args, ContextRequest: contextRequest, SkillID: primary.SkillID, SkillVersionID: primary.VersionID, RunMode: "read",
		OnStart: func(id string) {
			// The public callback records the child link before the run can wait for a slot.
			onChild(id)
			recordErr = a.Skills.RecordRunBindings(context.WithoutCancel(ctx), id, n.SkillBindings)
			if recordErr == nil {
				state := n.SkillSetState
				if state == "" {
					state = a.Skills.SourceState(ctx, n.SkillSetSource, n.SkillBindings)
				}
				recordErr = a.Skills.RecordRunSourceState(context.WithoutCancel(ctx), id, n.SkillSetSource, state)
			}
			if recordErr != nil {
				cancel()
			}
		},
	}, provider, 0)
	if recordErr != nil {
		return "", fmt.Errorf("cannot record skill versions: %w", recordErr)
	}
	if err != nil {
		return "", err
	}
	if res.Status != "succeeded" {
		return "", fmt.Errorf("node run %s: %s", res.Status, res.ErrorMessage)
	}
	return res.ResultText, nil
}
func (a *App) stopWorkflows() {
	if a.Workflows != nil {
		a.Workflows.BeginShutdown()
	}
}
func (a *App) waitWorkflows() {
	if a.Workflows == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownWriteWait)
	defer cancel()
	if err := a.Workflows.Wait(ctx); err != nil {
		a.Log.Warn("workflow shutdown", "error", err)
	}
}

// kept local to the workflow lifecycle: interrupted graphs are never replayed
// automatically, because a completed node may have performed external actions.
func (a *App) recoverWorkflows(ctx context.Context) error {
	if a.Workflows != nil {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := a.Workflows.Recover(c); err != nil {
			a.Log.Error("workflow recovery", "error", err)
			return err
		}
	}
	return nil
}
