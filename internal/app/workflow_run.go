package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func (a *App) initWorkflows() {
	a.Workflows = workflow.New(a.DB, a.executeWorkflowNode)
	a.Workflows.Prepare = a.prepareWorkflow
	a.Workflows.Admit = a.admitWorkflow
	a.Workflows.OverBudget = a.workflowOverBudget
	a.Workflows.OnError = func(err error) { a.Log.Error("workflow persistence", "error", err) }
}
func (a *App) prepareWorkflow(ctx context.Context, d workflow.Definition) (workflow.Definition, error) {
	// The folder is checked like any run's (symlinks resolved, sensitive folders
	// refused); the run's snapshot keeps the resolved path.
	if ws := guard.ExpandHome(d.WorkspaceDir); ws != "" {
		home, _ := os.UserHomeDir()
		real, prob := guard.CheckRunDir(ws, home, a.Cfg.HomeDir)
		if prob != "" {
			return d, &guard.DirError{Problem: prob}
		}
		d.WorkspaceDir = real
	}
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

// wfRun is what a workflow run holds while it goes: its shared scratch folder
// (edit / exec without a folder) and its nodes' runs (for the total budget).
type wfRun struct {
	mu       sync.Mutex
	scratch  string
	children []string
}

var wfRuns sync.Map // workflow run id → *wfRun

func wfRunOf(rid string) *wfRun {
	v, _ := wfRuns.LoadOrStore(rid, &wfRun{})
	return v.(*wfRun)
}

// admitWorkflow starts a run's hold on its folder: nodes that may change the
// owner's folder lock it for the whole run (one run changing a folder at a time,
// like any run), and edit / exec nodes without a folder share a scratch folder
// that is removed when the run ends.
func (a *App) admitWorkflow(_ context.Context, rid string, d workflow.Definition) (func(), error) {
	w := wfRunOf(rid)
	done := func() { wfRuns.Delete(rid) }
	if !d.Writes() {
		return done, nil
	}
	if d.WorkspaceDir != "" {
		if !lockEditDir(d.WorkspaceDir) {
			done()
			return nil, guard.ErrDirBusy
		}
		return func() { unlockEditDir(d.WorkspaceDir); done() }, nil
	}
	dir, err := os.MkdirTemp(a.runWorkDir(), "wfr-")
	if err != nil {
		done()
		return nil, err
	}
	w.mu.Lock()
	w.scratch = dir
	w.mu.Unlock()
	return func() { _ = os.RemoveAll(dir); done() }, nil
}

// workflowSpent is what the run's nodes cost so far, in the budget's currency.
func (a *App) workflowSpent(ctx context.Context, rid string, d workflow.Definition) (float64, error) {
	w := wfRunOf(rid)
	w.mu.Lock()
	ids := append([]string(nil), w.children...)
	w.mu.Unlock()
	f := a.cachedFX()
	total := 0.0
	for _, id := range ids {
		var amount float64
		var currency string
		err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(cost_amount,0),COALESCE(cost_currency,'USD') FROM runs WHERE id=?`, id).Scan(&amount, &currency)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += f.Convert(amount, currency, d.BudgetCurrency)
	}
	return total, nil
}

func (a *App) workflowOverBudget(ctx context.Context, rid string, d workflow.Definition) string {
	spent, err := a.workflowSpent(ctx, rid, d)
	if err != nil {
		return "cannot read what the run cost: " + err.Error()
	}
	if spent >= d.Budget {
		return fmt.Sprintf("spent %.2f of %.2f %s", spent, d.Budget, d.BudgetCurrency)
	}
	return ""
}

// executeWorkflowNode runs one skill node with the workflow's folder and permission
// level ("" = saved before the three levels: read-only, as before). Nodes carry no
// limits of their own: a node started by hand has only the global daily budget and
// the configured default timeout (none unless set); a task's node runs under the
// task's budgets (its run cap per node); the workflow's own budget caps what is left.
func (a *App) executeWorkflowNode(ctx context.Context, n workflow.Node, prompt, provider string, onChild func(string)) (string, error) {
	ri, _ := workflow.RunFrom(ctx)
	d := ri.Def
	mode := d.RunMode
	if mode == "" {
		mode = "read"
	}
	capUSD := 0.0
	if d.Budget > 0 && ri.RunID != "" {
		spent, err := a.workflowSpent(ctx, ri.RunID, d)
		if err != nil {
			return "", err
		}
		if spent >= d.Budget {
			return "", fmt.Errorf("%w: spent %.2f of %.2f %s", workflow.ErrBudget, spent, d.Budget, d.BudgetCurrency)
		}
		capUSD = a.cachedFX().Convert(d.Budget-spent, d.BudgetCurrency, pricing.USD)
	}
	st, err := a.stageSkills(ctx, n.SkillBindings)
	if err != nil {
		return "", err
	}
	defer st.Close()
	if st == nil {
		return "", fmt.Errorf("node %s has no skills", n.ID)
	}
	ws := d.WorkspaceDir
	if ws == "" && mode != "read" && ri.RunID != "" {
		w := wfRunOf(ri.RunID)
		w.mu.Lock()
		ws = w.scratch
		w.mu.Unlock()
	}
	if ws == "" { // read-only without a folder: an empty folder of its own, as before
		ws = filepath.Join(st.Dir, "work")
		if err := os.MkdirAll(ws, 0o700); err != nil {
			return "", err
		}
	}
	modern := a.modernClaude()
	args, err := st.CheckedArgs(modern, true)
	if err != nil {
		return "", err
	}
	if mode == "read" {
		args = append(args, "--disallowedTools", strings.Join(readOnlyDeny, ","))
	}
	contextRequest, err := workflowContext(st, n, modern, ws)
	if err != nil {
		return "", err
	}
	primary := n.SkillBindings[0]
	if n.ContextRunID != "" {
		var workflowID string
		if e := a.DB.QueryRowContext(ctx, `SELECT workflow_id FROM workflow_runs WHERE id=?`, n.ContextRunID).Scan(&workflowID); e != nil {
			return "", e
		}
		ctx = context.WithValue(ctx, capabilityOwnerKey{}, capabilityOwner{"workflow_node", workflowID + ":" + n.ID})
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	bucket := ri.Bucket
	if bucket == "" {
		bucket = cost.BucketProduction
	}
	var recordErr error
	onStart := func(id string) {
		// The public callback records the child link before the run can wait for a slot.
		onChild(id)
		if ri.RunID != "" {
			w := wfRunOf(ri.RunID)
			w.mu.Lock()
			w.children = append(w.children, id)
			w.mu.Unlock()
		}
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
	}
	opts := runner.Options{
		Prompt: st.Invoke(modern, prompt), PromptStdin: true, DisplayPrompt: prompt,
		WorkspaceDir: ws, TriggerKind: "workflow", TaskID: ri.TaskID, CostBucket: bucket, CapUSD: capUSD,
		ContextArgs: args, ContextRequest: contextRequest, SkillID: primary.SkillID, SkillVersionID: primary.VersionID, RunMode: mode,
		OnStart: onStart,
	}
	if mode != "read" && d.WorkspaceDir != "" {
		// what the node changed in the owner's folder (git work trees), for its run page
		tr := &changeTracker{a: a, dir: ws}
		defer tr.close()
		opts.OnStart, opts.AtStart, opts.AfterExit = tr.onStart(onStart), tr.atStart, tr.afterExit
	}
	res, err := a.RunPromptEx(childCtx, opts, provider, 0)
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

// runTaskWorkflow runs a workflow task: the task's prompt is the workflow's input,
// its budgets apply to every node's run, and its timeout to the whole workflow run.
func (a *App) runTaskWorkflow(ctx context.Context, task db.Task, spec tasksvc.Spec, provider, bucket string, onStart func(string)) error {
	if a.Workflows == nil {
		return errors.New("workflows unavailable")
	}
	rid, err := a.Workflows.StartWith(ctx, workflow.StartOptions{WorkflowID: spec.WorkflowID, Version: spec.WorkflowVersion,
		Input: externalPrompt(ctx, spec.Prompt), Provider: provider, TaskID: task.ID, Bucket: bucket})
	if err != nil {
		return fmt.Errorf("workflow: %w", err)
	}
	if onStart != nil {
		onStart(rid)
	}
	wait := ctx
	if spec.TimeoutMinutes > 0 {
		var done context.CancelFunc
		wait, done = context.WithTimeout(ctx, time.Duration(spec.TimeoutMinutes)*time.Minute)
		defer done()
	}
	if err := a.Workflows.WaitRun(wait, rid); err != nil {
		_ = a.Workflows.Stop(rid)
		c, done := context.WithTimeout(context.Background(), 30*time.Second)
		_ = a.Workflows.WaitRun(c, rid)
		done()
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("workflow run %s: task timeout (%d min)", rid, spec.TimeoutMinutes)
		}
		return err
	}
	r, err := a.Workflows.GetRun(context.WithoutCancel(ctx), rid)
	if err != nil {
		return err
	}
	if r.Status != "succeeded" {
		return fmt.Errorf("workflow run %s: %s", r.Status, r.Error)
	}
	return nil
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
