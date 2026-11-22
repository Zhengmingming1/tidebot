package webui

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// TestMissingWorkspacesWorkflows: after a restore, live workflows whose current
// definition names a folder missing on this computer are listed next to tasks;
// "~" paths are expanded like a run does; deleted workflows, workflows without
// a folder and older versions' folders are ignored; saved definitions are not
// rewritten; a list with only workflows still keeps the notice pending.
func TestMissingWorkspacesWorkflows(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	appHome := t.TempDir()
	c.s.deps.Cfg.HomeDir = appHome
	svc := workflow.New(c.s.deps.DB, func(context.Context, workflow.Node, string, string, func(string)) (string, error) {
		return "", nil
	})
	c.s.deps.Workflows = svc

	if err := os.MkdirAll(filepath.Join(userHome, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	here := t.TempDir()
	gone := filepath.Join(t.TempDir(), "old-machine", "site")
	bind := []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}
	save := func(wid string, ver int, name, ws string) workflow.Workflow {
		t.Helper()
		w, err := svc.Save(ctx, wid, ver, workflow.Definition{Name: name, WorkspaceDir: ws,
			Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: bind}}})
		if err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
		return w
	}
	none := save("", 0, "No folder", "")
	ok := save("", 0, "Here", here)
	tilde := save("", 0, "Tilde", "~/proj")
	tildeGone := save("", 0, "Tilde gone", "~/nope")
	miss := save("", 0, "Pipe <x>", gone)
	del := save("", 0, "Deleted", gone)
	if err := svc.Delete(ctx, del.ID); err != nil {
		t.Fatal(err)
	}
	// An older version pointed at a missing folder; the current one is fine.
	moved := save("", 0, "Moved", gone)
	moved = save(moved.ID, moved.Version, "Moved", here)
	var before string
	if err := c.s.deps.DB.QueryRow(`SELECT definition_json FROM workflow_versions WHERE workflow_id=? AND version=1`, miss.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	if err := MarkMissingWorkspaceNotice(ctx, c.s.deps.DB); err != nil {
		t.Fatal(err)
	}
	code, out := c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if code != 200 || out["pending"] != true {
		t.Fatalf("pending with only workflows: %d %v", code, out)
	}
	if tasks, _ := out["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("tasks %v", tasks)
	}
	got := map[string]map[string]any{}
	for _, raw := range out["workflows"].([]any) {
		row := raw.(map[string]any)
		got[row["id"].(string)] = row
	}
	for _, id := range []string{none.ID, ok.ID, tilde.ID, del.ID, moved.ID} {
		if row, bad := got[id]; bad {
			t.Errorf("listed %v", row)
		}
	}
	if row := got[miss.ID]; row == nil || row["name"] != "Pipe <x>" || row["workspace_dir"] != gone {
		t.Errorf("missing workflow folder: %v", row)
	}
	if row := got[tildeGone.ID]; row == nil || row["workspace_dir"] != "~/nope" {
		t.Errorf("missing ~ folder: %v", row)
	}
	if len(got) != 2 {
		t.Errorf("listed %d workflows, want 2: %v", len(got), got)
	}
	var after string
	if err := c.s.deps.DB.QueryRow(`SELECT definition_json FROM workflow_versions WHERE workflow_id=? AND version=1`, miss.ID).Scan(&after); err != nil || after != before {
		t.Fatalf("definition rewritten: %s %v", after, err)
	}

	// A task saved with "~/proj" exists on this computer and is not listed.
	code, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Tilde task", "prompt": "p", "provider": "anthropic", "workspace": "~/proj"})
	if code != 201 {
		t.Fatalf("create task %d %v", code, tk)
	}
	if _, err := c.s.deps.DB.Exec(`UPDATE tasks SET workspace_dir='~/proj' WHERE id=?`, tk["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.deps.DB.Exec(`UPDATE versions SET content_json=json_set(content_json,'$.workspace_dir','~/proj')
WHERE id=(SELECT version_id FROM pointers WHERE entity_type='task' AND entity_id=? AND name='current')`, tk["id"]); err != nil {
		t.Fatal(err)
	}
	_, out = c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if tasks, _ := out["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("~ task folder that exists listed: %v", tasks)
	}

	// Once every workflow folder exists, GET clears the notice itself.
	for _, p := range []string{gone, filepath.Join(userHome, "nope")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	code, out = c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if code != 200 || out["pending"] != false {
		t.Fatalf("auto-dismiss: %d %v", code, out)
	}
	if flows, ok := out["workflows"].([]any); !ok || len(flows) != 0 {
		t.Fatalf("workflows should be an empty array: %v", out["workflows"])
	}
}
