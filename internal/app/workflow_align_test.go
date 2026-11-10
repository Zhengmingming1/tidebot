package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func wfDone(t *testing.T, a *App, rid string) workflow.Run {
	t.Helper()
	var r workflow.Run
	var err error
	waitFor(t, 15*time.Second, func() bool {
		r, err = a.Workflows.GetRun(context.Background(), rid)
		return err == nil && r.Status != "queued" && r.Status != "running"
	})
	return r
}

func twoNodes(sid string) []workflow.Node {
	b := []tasksvc.SkillBinding{{SkillID: sid, Mode: "follow", Role: "primary"}}
	return []workflow.Node{
		{ID: "a", Kind: "skill", Prompt: "first {{input}}", SkillBindings: b},
		{ID: "b", Kind: "skill", Prompt: "next {{steps.a.output}}", SkillBindings: b, Needs: []workflow.Dependency{{NodeID: "a"}}},
	}
}

// A command workflow without a folder: every node works in one scratch folder of the
// run (removed afterwards), runs commands (no --restricted) and carries no hidden
// per-node cap or time limit.
func TestWorkflowExecSharesScratchAndHasNoHiddenLimits(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	sid := importTestSkill(t, a, "lead")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	w, err := a.Workflows.Save(context.Background(), "", 0, workflow.Definition{Name: "Flow", RunMode: "exec", Nodes: twoNodes(sid)})
	if err != nil {
		t.Fatal(err)
	}
	rid, err := a.Workflows.Start(context.Background(), w.ID, "go", "kimi-coding")
	if err != nil {
		t.Fatal(err)
	}
	r := wfDone(t, a, rid)
	if r.Status != "succeeded" {
		t.Fatalf("%s %s", r.Status, r.Error)
	}
	dirs := map[string]bool{}
	for _, n := range r.Nodes {
		var mode, ws string
		if err := a.DB.QueryRow(`SELECT run_mode,workspace_dir FROM runs WHERE id=?`, n.ChildRunID).Scan(&mode, &ws); err != nil {
			t.Fatal(err)
		}
		if mode != "exec" || !strings.HasPrefix(ws, filepath.Join(a.Cfg.DataDir, "run-work")) {
			t.Fatalf("node %s: %s %s", n.NodeID, mode, ws)
		}
		dirs[ws] = true
	}
	if len(dirs) != 1 {
		t.Fatalf("nodes did not share the scratch folder: %v", dirs)
	}
	for d := range dirs {
		waitFor(t, 5*time.Second, func() bool { _, err := os.Stat(d); return os.IsNotExist(err) })
	}
	args := readArgs(t, argsFile)
	if contains(args, "--restricted") || contains(args, "--max-budget-usd") {
		t.Fatalf("hidden limit or restriction: %v", args)
	}
}

// An edit workflow in the owner's folder: nodes work there, the folder is held for
// the whole run, and sensitive folders are refused when saving.
func TestWorkflowFolderEditAndChecks(t *testing.T) {
	a := newTestApp(t)
	home := fakeHome(t)
	sid := importTestSkill(t, a, "lead")
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	w, err := a.Workflows.Save(context.Background(), "", 0, workflow.Definition{Name: "Flow", RunMode: "edit", WorkspaceDir: dir, Nodes: twoNodes(sid)})
	if err != nil {
		t.Fatal(err)
	}
	if !lockEditDir(real) {
		t.Fatal("folder already held")
	}
	if _, err := a.Workflows.Start(context.Background(), w.ID, "go", "kimi-coding"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy folder started: %v", err)
	}
	unlockEditDir(real)
	rid, err := a.Workflows.Start(context.Background(), w.ID, "go", "kimi-coding")
	if err != nil {
		t.Fatal(err)
	}
	r := wfDone(t, a, rid)
	if r.Status != "succeeded" || r.Snapshot.WorkspaceDir != real {
		t.Fatalf("%s %s %q", r.Status, r.Error, r.Snapshot.WorkspaceDir)
	}
	for _, n := range r.Nodes {
		var mode, ws string
		_ = a.DB.QueryRow(`SELECT run_mode,workspace_dir FROM runs WHERE id=?`, n.ChildRunID).Scan(&mode, &ws)
		if mode != "edit" || ws != real {
			t.Fatalf("node %s: %s %s", n.NodeID, mode, ws)
		}
	}
	if !lockEditDir(real) {
		t.Fatal("folder still held after the run")
	}
	unlockEditDir(real)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	if _, err := a.Workflows.Save(context.Background(), "", 0, workflow.Definition{Name: "Bad", RunMode: "read", WorkspaceDir: filepath.Join(home, ".ssh"), Nodes: twoNodes(sid)}); err == nil {
		t.Fatal("sensitive folder accepted")
	}
	// Saved before the three levels: read-only, an empty folder per node, as before.
	old, err := a.Workflows.Save(context.Background(), "", 0, workflow.Definition{Name: "Old", Nodes: twoNodes(sid)})
	if err != nil {
		t.Fatal(err)
	}
	rid, _ = a.Workflows.Start(context.Background(), old.ID, "go", "kimi-coding")
	r = wfDone(t, a, rid)
	for _, n := range r.Nodes {
		var mode, ws string
		_ = a.DB.QueryRow(`SELECT run_mode,workspace_dir FROM runs WHERE id=?`, n.ChildRunID).Scan(&mode, &ws)
		if mode != "read" || !strings.HasPrefix(ws, a.skillStageRoot()) {
			t.Fatalf("old workflow node %s: %s %s", n.NodeID, mode, ws)
		}
	}
}

// A workflow budget: a node starts capped at what is left, and once the nodes'
// runs cost the budget (in its currency) no further node starts.
func TestWorkflowTotalBudget(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ctx := context.Background()
	sid := importTestSkill(t, a, "lead")
	w, err := a.Workflows.Save(ctx, "", 0, workflow.Definition{Name: "Flow", RunMode: "read", Budget: 2, BudgetCurrency: "USD", Nodes: twoNodes(sid)})
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := a.Workflows.Start(ctx, w.ID, "go", "kimi-coding")
	if r := wfDone(t, a, rid); r.Status != "succeeded" {
		t.Fatalf("%s %q", r.Status, r.Error)
	}
	if runCallerCap(runner.Options{CapUSD: 1.5}) != 1.5 || runCallerCap(runner.Options{CapUSD: 3, ExtraArgs: []string{"--max-budget-usd", "2"}}) != 2 || runCallerCap(runner.Options{}) != 0 {
		t.Fatal("caller cap")
	}
	// What the nodes' runs cost, across currencies.
	d := workflow.Definition{Budget: 2, BudgetCurrency: "USD"}
	for i, c := range []struct {
		amount   float64
		currency string
	}{{1.5, "USD"}, {7.1, "CNY"}} {
		id := "run_budget_" + string(rune('a'+i))
		if _, err := a.DB.Exec(`INSERT INTO runs(id,status,prompt,cost_amount,cost_currency,created_at) VALUES(?,'succeeded','x',?,?,?)`, id, c.amount, c.currency, nowUTC()); err != nil {
			t.Fatal(err)
		}
		w := wfRunOf("wfr_budget")
		w.children = append(w.children, id)
		why := a.workflowOverBudget(ctx, "wfr_budget", d)
		if i == 0 && why != "" || i == 1 && !strings.Contains(why, "of 2.00 USD") {
			t.Fatalf("step %d: %q", i, why)
		}
	}
	wfRuns.Delete("wfr_budget")
}

// A task can run a workflow: the prompt is the input, the nodes run under the task
// (its budgets apply), and the run id handed back is the workflow run's.
func TestTaskRunsWorkflow(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ctx := context.Background()
	sid := importTestSkill(t, a, "lead")
	w, err := a.Workflows.Save(ctx, "", 0, workflow.Definition{Name: "Flow", RunMode: "read", Nodes: twoNodes(sid)})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "Nightly", WorkflowID: w.ID, WorkflowVersion: 1, Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if tv.Spec.WorkflowID != w.ID || tv.Spec.WorkflowVersion != 1 {
		t.Fatalf("spec %+v", tv.Spec)
	}
	bad := *tv.Spec
	bad.SkillBindings = []tasksvc.SkillBinding{{SkillID: sid, Mode: "follow", Role: "primary"}}
	if _, err := a.Tasks.UpdateSpec(ctx, tv.Task.ID, bad, "user", "x"); err == nil {
		t.Fatal("workflow task with skills accepted")
	}
	spec := *tv.Spec
	spec.Prompt = "tonight's input"
	if _, err := a.Tasks.UpdateSpec(ctx, tv.Task.ID, spec, "user", "input"); err != nil {
		t.Fatal(err)
	}
	var started string
	if err := a.runTaskTriggerOn(ctx, tv.Task, "manual", "", func(id string) { started = id }); err != nil {
		t.Fatal(err)
	}
	r, err := a.Workflows.GetRun(ctx, started)
	if err != nil || r.Status != "succeeded" || r.Input != "tonight's input" || r.WorkflowVersion != 1 {
		t.Fatalf("run %q: %+v %v", started, r, err)
	}
	for _, n := range r.Nodes {
		var task string
		_ = a.DB.QueryRow(`SELECT COALESCE(task_id,'') FROM runs WHERE id=?`, n.ChildRunID).Scan(&task)
		if task != tv.Task.ID {
			t.Fatalf("node %s ran outside the task: %q", n.NodeID, task)
		}
	}
	// A failed workflow fails the task run.
	if err := a.Workflows.Delete(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.runTaskTriggerOn(ctx, tv.Task, "manual", "", nil); err == nil {
		t.Fatal("deleted workflow ran")
	}
}
