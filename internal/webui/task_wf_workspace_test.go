package webui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// TestWorkflowTaskWorkspaceIsWorkflows: a workflow task's runs work in the
// folder of the workflow version it runs, so the detail answer shows that
// folder ("~" expanded like the run) instead of the task's own or its default
// one; "Open folder" opens it without creating anything (a workflow without a
// folder has nothing to open, and the unused default folder is not created);
// the after-restore notice does not list a workflow task for its own folder.
func TestWorkflowTaskWorkspaceIsWorkflows(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	svc := workflow.New(c.s.deps.DB, func(context.Context, workflow.Node, string, string, func(string)) (string, error) {
		return "", nil
	})
	c.s.deps.Workflows = svc
	v1Dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userHome, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	bind := []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}
	save := func(wid string, ver int, ws string) workflow.Workflow {
		t.Helper()
		w, err := svc.Save(ctx, wid, ver, workflow.Definition{Name: "Flow", WorkspaceDir: ws,
			Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: bind}}})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	w1 := save("", 0, v1Dir)
	save(w1.ID, w1.Version, "~/proj")
	bare := save("", 0, "")
	mk := func(name string, wf map[string]any) string {
		t.Helper()
		code, out := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": name, "provider": "anthropic", "workflow": wf})
		if code >= 300 {
			t.Fatalf("create %s: %d %v", name, code, out)
		}
		return fmt.Sprint(out["id"])
	}
	pinID := mk("Pinned", map[string]any{"id": w1.ID, "version": w1.Version})
	followID := mk("Follows", map[string]any{"id": w1.ID, "version": 0})
	bareID := mk("Bare", map[string]any{"id": bare.ID, "version": 0})
	// A stale folder of the task's own (from before it became a workflow task,
	// or a backup from another computer) is never what the runs use.
	stale := filepath.Join(t.TempDir(), "old-machine", "proj")
	if _, err := c.s.deps.DB.Exec(`UPDATE tasks SET workspace_dir=?`, stale); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{pinID: v1Dir, followID: filepath.Join(userHome, "proj"), bareID: ""}
	for id, ws := range want {
		code, d := c.json(http.MethodGet, "/api/tasks/"+id, nil)
		if code != 200 {
			t.Fatalf("get %s: %d", id, code)
		}
		if got, _ := d["workspace"].(string); got != ws || d["workspace_workflow"] != true || d["workspace_default"] != nil {
			t.Errorf("detail %s: workspace %q workflow %v default %v, want %q", d["name"], got, d["workspace_workflow"], d["workspace_default"], ws)
		}
	}
	// A prompt task keeps its own folder and no workflow mark.
	code, out := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Plain", "prompt": "p", "provider": "anthropic"})
	if code >= 300 {
		t.Fatalf("create plain: %d %v", code, out)
	}
	plainID := fmt.Sprint(out["id"])
	if _, err := c.s.deps.DB.Exec(`UPDATE tasks SET workspace_dir=? WHERE id=?`, stale, plainID); err != nil {
		t.Fatal(err)
	}
	if _, d := c.json(http.MethodGet, "/api/tasks/"+plainID, nil); d["workspace_workflow"] != nil || d["workspace"] != stale {
		t.Errorf("plain task: %v", d)
	}

	var opened []string
	c.s.OpenPath = func(p string, reveal bool) error { opened = append(opened, p); return nil }
	for _, id := range []string{pinID, followID} {
		if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "workspace", "id": id}); code != 200 {
			t.Fatalf("open %s: %d", id, code)
		}
	}
	if len(opened) != 2 || opened[0] != v1Dir || opened[1] != filepath.Join(userHome, "proj") {
		t.Fatalf("opened %v", opened)
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "workspace", "id": bareID}); code != 404 || len(opened) != 2 {
		t.Fatalf("workflow without a folder: %d %v", code, opened)
	}
	for _, id := range []string{pinID, followID, bareID} {
		if _, err := os.Stat(guard.TaskWorkspace(c.s.deps.Cfg.HomeDir, id)); !os.IsNotExist(err) {
			t.Errorf("unused default folder of %s created: %v", id, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale task folder created: %v", err)
	}

	// After a restore only the prompt task is listed for the stale folder.
	if err := MarkMissingWorkspaceNotice(ctx, c.s.deps.DB); err != nil {
		t.Fatal(err)
	}
	code, out = c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	tasks, _ := out["tasks"].([]any)
	if code != 200 || len(tasks) != 1 || tasks[0].(map[string]any)["id"] != plainID {
		t.Fatalf("missing workspaces: %d %v", code, out)
	}
}

// TestWorkflowTaskWorkspaceUI: the overview marks a workflow task's folder as
// the workflow's, says so when the workflow names none, keeps the folder pill
// next to the path, and the new strings exist in both languages.
func TestWorkflowTaskWorkspaceUI(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	for _, s := range []string{`T("ws.wf_tip")`, `T("ws.wf")`, `T("ws.wf_none_tip")`, `T("ws.wf_none")`, `tk.workspace_workflow`} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		d := loadDict(t, lang)
		for _, k := range []string{"ws.wf", "ws.wf_tip", "ws.wf_none", "ws.wf_none_tip"} {
			if strings.TrimSpace(d[k]) == "" {
				t.Errorf("%s: %s missing", lang, k)
			}
		}
	}
}
