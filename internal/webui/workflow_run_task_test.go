package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// A workflow run started by a task keeps that task on the run (and the list),
// with the task's name for the page; a hand-started run has no task; a missing
// task still has the id so the page can say it was deleted.
func TestWorkflowRunTaskAPI(t *testing.T) {
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
	d := workflow.Definition{Name: "Nightly flow", Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}}}}
	code, out := c.json(http.MethodPost, "/api/workflows", d)
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	wid := out["id"].(string)
	code, task := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Every night", "prompt": "",
		"workflow": map[string]any{"id": wid, "version": 1}})
	if code != 201 {
		t.Fatalf("task %d %v", code, task)
	}
	tid := task["id"].(string)
	fromTask, err := c.s.deps.Workflows.StartWith(context.Background(), workflow.StartOptions{WorkflowID: wid, Input: "from task", Provider: "kimi-coding", TaskID: tid})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := c.s.deps.Workflows.StartWith(context.Background(), workflow.StartOptions{WorkflowID: wid, Input: "gone", Provider: "kimi-coding", TaskID: "tsk_gone"})
	if err != nil {
		t.Fatal(err)
	}
	code, hand := c.json(http.MethodPost, "/api/workflows/"+wid+"/run", map[string]string{"input": "by hand", "provider": "kimi-coding"})
	if code != 202 {
		t.Fatalf("hand run %d %v", code, hand)
	}
	wait := func(id string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		var row map[string]any
		for time.Now().Before(deadline) {
			_, row = c.json(http.MethodGet, "/api/workflow-runs/"+id, nil)
			if row["status"] == "succeeded" {
				return row
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("run %s did not finish: %v", id, row)
		return row
	}
	row := wait(fromTask)
	if row["task_id"] != tid || row["task_name"] != "Every night" {
		t.Fatalf("task run %v", row)
	}
	row = wait(gone)
	if row["task_id"] != "tsk_gone" || row["task_name"] != nil {
		t.Fatalf("gone task %v", row)
	}
	row = wait(hand["run_id"].(string))
	if row["task_id"] != "" || row["task_name"] != nil {
		t.Fatalf("hand run %v", row)
	}
	rr := c.do(http.MethodGet, "/api/workflow-runs?workflow_id="+wid, "", nil)
	var list []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list) != 3 {
		t.Fatalf("list %s %v", rr.Body.String(), err)
	}
	byID := map[string]map[string]any{}
	for _, r := range list {
		byID[r["id"].(string)] = r
	}
	if r := byID[fromTask]; r["task_id"] != tid || r["task_name"] != "Every night" {
		t.Fatalf("list task run %v", r)
	}
	if r := byID[gone]; r["task_id"] != "tsk_gone" || r["task_name"] != nil {
		t.Fatalf("list gone %v", r)
	}
	if r := byID[hand["run_id"].(string)]; r["task_id"] != "" || r["task_name"] != nil {
		t.Fatalf("list hand %v", r)
	}
}

// The history and execution pages show the starting task (or that it was deleted).
func TestWorkflowRunTaskUI(t *testing.T) {
	js := appJSSource(t)
	for _, want := range []string{
		"function wfRunTask(r)",
		`href="#/tasks/' + enc(r.task_id) + '"`,
		"T('wf.from_task_tip')",
		"T('wf.task_gone')",
		"T('wf.task_gone_tip')",
		"+wfRunTask(x)+wfStatus(x.status)",
		"+wfRunTask(r)+'<span class=\"muted\">'+esc(fmtTime(r.created_at))",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range []string{"wf.from_task_tip", "wf.task_gone", "wf.task_gone_tip"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
