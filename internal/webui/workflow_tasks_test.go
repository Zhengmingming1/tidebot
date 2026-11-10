package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// A workflow gets its schedule and budgets from a task that runs it: the task form
// takes the workflow (pinned version), the workflow list shows the tasks, and the
// task moves through export / import.
func TestWorkflowTaskAPI(t *testing.T) {
	c := login(t)
	c.s.deps.Workflows = workflow.New(c.s.deps.DB, func(ctx context.Context, n workflow.Node, prompt, provider string, _ func(string)) (string, error) {
		return prompt, nil
	})
	t.Cleanup(func() {
		c.s.deps.Workflows.BeginShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.s.deps.Workflows.Wait(ctx)
	})
	d := workflow.Definition{Name: "Nightly flow", RunMode: "exec", Budget: 3, BudgetCurrency: "USD", Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}}}}
	code, out := c.json(http.MethodPost, "/api/workflows", d)
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	wid := out["id"].(string)
	if def := out["definition"].(map[string]any); def["run_mode"] != "exec" || def["budget"] != 3.0 || def["budget_currency"] != "USD" {
		t.Fatalf("settings not kept: %v", def)
	}
	// Workflow tasks need no prompt; prompt tasks still do; unknown workflows are refused.
	if code, _ := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "No prompt", "prompt": ""}); code != 400 {
		t.Fatalf("prompt task without prompt: %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Bad", "workflow": map[string]any{"id": "wf_nope", "version": 1}}); code != 400 {
		t.Fatalf("unknown workflow: %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Bad", "workflow": map[string]any{"id": wid, "version": 7}}); code != 400 {
		t.Fatalf("unknown version: %d", code)
	}
	code, task := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Every night", "prompt": "", "cron": "0 2 * * *", "enabled": true,
		"workflow": map[string]any{"id": wid, "version": 1}, "skill_bindings": []map[string]any{{"skill_id": "x", "mode": "follow"}}})
	if code != 201 {
		t.Fatalf("workflow task %d %v", code, task)
	}
	tid := task["id"].(string)
	if wf := task["workflow"].(map[string]any); wf["id"] != wid || wf["version"] != 1.0 || len(task["skills"].([]any)) != 0 {
		t.Fatalf("task %v", task)
	}
	var list []map[string]any
	rr := c.do(http.MethodGet, "/api/workflows", "", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if len(list) != 1 || len(list[0]["tasks"].([]any)) != 1 || list[0]["tasks"].([]any)[0].(map[string]any)["cron"] != "0 2 * * *" {
		t.Fatalf("workflow list %s", rr.Body.String())
	}
	if code, one := c.json(http.MethodGet, "/api/workflows/"+wid, nil); code != 200 || len(one["tasks"].([]any)) != 1 || one["definition"] == nil {
		t.Fatalf("workflow %d %v", code, one)
	}
	var tasks []map[string]any
	rr = c.do(http.MethodGet, "/api/tasks", "", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &tasks)
	if len(tasks) != 1 || tasks[0]["workflow"].(map[string]any)["name"] != "Nightly flow" {
		t.Fatalf("task list %s", rr.Body.String())
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+tid+"/form", nil)
	fm := form["form"].(map[string]any)
	if wf := fm["workflow"].(map[string]any); wf["id"] != wid || wf["name"] != "Nightly flow" || wf["current"] != 1.0 {
		t.Fatalf("form %v", fm)
	}
	// Editing from an older page (no workflow field) keeps the target.
	fm["prompt"] = "tonight"
	delete(fm, "workflow")
	if code, out := c.json(http.MethodPut, "/api/tasks/"+tid, fm); code != 200 || out["workflow"] == nil || out["prompt"] != "tonight" {
		t.Fatalf("edit %d %v", code, out)
	}
	// Export / import: the same workflow (id) and its pinned version.
	rr = c.do(http.MethodGet, "/api/tasks/export?ids="+tid, "", nil)
	var f taskFile
	if err := json.Unmarshal(rr.Body.Bytes(), &f); err != nil || f.Tasks[0].Workflow == nil || f.Tasks[0].Workflow.Name != "Nightly flow" || f.Tasks[0].Workflow.Version != 1 {
		t.Fatalf("export %v %s", err, rr.Body.String())
	}
	raw, _ := json.Marshal(f)
	code, out = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": json.RawMessage(raw), "apply": true})
	row := out["tasks"].([]any)[0].(map[string]any)
	if code != 200 || row["task_id"] == nil {
		t.Fatalf("import %d %v", code, out)
	}
	_, form = c.json(http.MethodGet, "/api/tasks/"+row["task_id"].(string)+"/form", nil)
	if wf := form["form"].(map[string]any)["workflow"].(map[string]any); wf["id"] != wid || wf["version"] != 1.0 {
		t.Fatalf("imported %v", wf)
	}
	// Another machine: found by name, following the current version; missing = error.
	f.Tasks[0].Workflow.ID = "wf_elsewhere"
	raw, _ = json.Marshal(f)
	_, out = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": json.RawMessage(raw)})
	if notes, _ := json.Marshal(out["tasks"].([]any)[0].(map[string]any)["notes"]); !strings.Contains(string(notes), "workflow_current") {
		t.Fatalf("by name %v", out)
	}
	f.Tasks[0].Workflow.Name = "Gone"
	raw, _ = json.Marshal(f)
	if _, out = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": json.RawMessage(raw)}); out["tasks"].([]any)[0].(map[string]any)["error"] == nil {
		t.Fatalf("missing workflow imported %v", out)
	}
	// Folder problems answer like the Run dialog's.
	w := httptest.NewRecorder()
	workflowError(w, &guard.DirError{Problem: "sensitive"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"problem":"sensitive"`) {
		t.Fatalf("dir error %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	workflowError(w, guard.ErrDirBusy)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"problem":"busy"`) {
		t.Fatalf("busy %d %s", w.Code, w.Body.String())
	}
}
