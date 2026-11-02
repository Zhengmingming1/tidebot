package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func TestWorkflowRunsUseFrozenSkillsAndIsolatedReadOnlyStages(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	primary := importTestSkill(t, a, "lead")
	helper := importTestSkill(t, a, "helper")
	bindings := []tasksvc.SkillBinding{{SkillID: primary, Mode: "follow", Role: "primary"}, {SkillID: helper, Mode: "follow", Role: "auxiliary"}}
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	w, err := a.Workflows.Save(context.Background(), "", 0, workflow.Definition{Name: "Flow", Nodes: []workflow.Node{
		{ID: "a", Kind: "skill", Prompt: "first {{input}}", SkillBindings: bindings},
		{ID: "b", Kind: "skill", Prompt: "next {{steps.a.output}}", SkillBindings: bindings, Needs: []workflow.Dependency{{NodeID: "a"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Save preserves follow; each run snapshots pinned bindings before execution.
	if w.Definition.Nodes[0].SkillBindings[0].Mode != "follow" {
		t.Fatal("saving unexpectedly froze follow binding")
	}
	rid, err := a.Workflows.Start(context.Background(), w.ID, "\"unicode 潮汐\"", "kimi-coding")
	if err != nil {
		t.Fatal(err)
	}
	var r workflow.Run
	waitFor(t, 15*time.Second, func() bool {
		r, err = a.Workflows.GetRun(context.Background(), rid)
		return err == nil && r.Status != "queued" && r.Status != "running"
	})
	if r.Status != "succeeded" || len(r.Nodes) != 2 || !strings.Contains(r.Nodes[1].Output, "first \"unicode 潮汐\"") {
		t.Fatalf("workflow failed: %+v", r)
	}
	workspaces := map[string]bool{}
	for _, node := range r.Nodes {
		if node.ChildRunID == "" {
			t.Fatal("missing child link")
		}
		var mode, ws, trigger, prompt string
		if err := a.DB.QueryRow(`SELECT run_mode,workspace_dir,trigger_kind,prompt FROM runs WHERE id=?`, node.ChildRunID).Scan(&mode, &ws, &trigger, &prompt); err != nil {
			t.Fatal(err)
		}
		if mode != "read" || trigger != "workflow" || !strings.HasPrefix(ws, a.skillStageRoot()) || workspaces[ws] {
			t.Fatalf("unsafe/shared workspace %s %s %s", mode, trigger, ws)
		}
		workspaces[ws] = true
		stored, err := a.Skills.RunBindings(context.Background(), node.ChildRunID)
		if err != nil || len(stored) != 2 {
			t.Fatalf("missing skill snapshot: %v %v", stored, err)
		}
		for _, b := range stored {
			if b.Mode != "pinned" || b.VersionID == "" {
				t.Fatalf("not frozen: %+v", b)
			}
		}
		if _, err := os.Stat(ws); !os.IsNotExist(err) {
			t.Fatal("stage left behind")
		}
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--disallowedTools\nEdit,Write,MultiEdit,NotebookEdit,Bash,PowerShell") || !strings.Contains(string(args), "--append-system-prompt-file") || strings.Contains(string(args), "unicode") {
		t.Fatalf("permissions/auxiliary/stdin missing: %s", args)
	}
}

func TestWorkflowPrepareFreezesAllNodesAndRejectsForeignVersion(t *testing.T) {
	a := newTestApp(t)
	sid := importTestSkill(t, a, "original")
	other := importTestSkill(t, a, "other")
	binding := tasksvc.SkillBinding{SkillID: sid, Mode: "follow"}
	d := workflow.Definition{Name: "Frozen", Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "a", SkillBindings: []tasksvc.SkillBinding{binding}}, {ID: "b", Kind: "skill", Prompt: "b", SkillBindings: []tasksvc.SkillBinding{binding}}}}
	frozen, err := a.prepareWorkflow(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	vid := frozen.Nodes[0].SkillBindings[0].VersionID
	if vid == "" || frozen.Nodes[1].SkillBindings[0].VersionID != vid || d.Nodes[0].SkillBindings[0].Mode != "follow" {
		t.Fatal("unstable or mutated snapshot")
	}
	d.Nodes[0].SkillBindings = []tasksvc.SkillBinding{{SkillID: other, Mode: "pinned", VersionID: vid}}
	if _, err := a.prepareWorkflow(context.Background(), d); err == nil {
		t.Fatal("foreign version accepted")
	}
}
