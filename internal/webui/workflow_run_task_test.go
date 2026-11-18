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

// The task page lists workflow executions by task_id, not only node runs, so a
// workflow that failed before any node started still appears. Other tasks and
// hand-started runs stay out; a skill task gets an empty list.
func TestTaskWorkflowRunsAPI(t *testing.T) {
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
	code, other := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Other", "prompt": "",
		"workflow": map[string]any{"id": wid, "version": 1}})
	if code != 201 {
		t.Fatalf("other task %d %v", code, other)
	}
	oid := other["id"].(string)
	code, skill := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Skill only", "prompt": "do it"})
	if code != 201 {
		t.Fatalf("skill task %d %v", code, skill)
	}
	sid := skill["id"].(string)

	first, err := c.s.deps.Workflows.StartWith(context.Background(), workflow.StartOptions{WorkflowID: wid, Input: "first", Provider: "kimi-coding", TaskID: tid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.s.deps.Workflows.StartWith(context.Background(), workflow.StartOptions{WorkflowID: wid, Input: "other", Provider: "kimi-coding", TaskID: oid}); err != nil {
		t.Fatal(err)
	}
	if code, hand := c.json(http.MethodPost, "/api/workflows/"+wid+"/run", map[string]string{"input": "by hand", "provider": "kimi-coding"}); code != 202 {
		t.Fatalf("hand run %d %v", code, hand)
	}
	wait := func(id string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		var row map[string]any
		for time.Now().Before(deadline) {
			_, row = c.json(http.MethodGet, "/api/workflow-runs/"+id, nil)
			if row["status"] == "succeeded" {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("run %s did not finish: %v", id, row)
	}
	wait(first)

	// A workflow run that never produced a node run still belongs on the task.
	early := "wfr_early"
	_, err = c.s.deps.DB.Exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,provider,snapshot_json,error,created_at,started_at,finished_at,task_id) VALUES(?,?,1,'failed','early input','kimi-coding',?,'workflow failed before any node started',?,?,?,?)`,
		early, wid, `{"name":"Nightly flow"}`, "2026-11-18T20:00:00Z", "2026-11-18T20:00:01Z", "2026-11-18T20:00:02Z", tid)
	if err != nil {
		t.Fatal(err)
	}

	rr := c.do(http.MethodGet, "/api/tasks/"+tid+"/workflow-runs?limit=10", "", nil)
	var list []map[string]any
	if rr.Code != 200 {
		t.Fatalf("list %d %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode %s %v", rr.Body.String(), err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 runs for this task, got %d %v", len(list), list)
	}
	byID := map[string]map[string]any{}
	for _, r := range list {
		byID[r["id"].(string)] = r
		if r["task_id"] != tid {
			t.Fatalf("foreign run on this task: %v", r)
		}
	}
	if _, ok := byID[first]; !ok {
		t.Fatalf("missing started run %s in %v", first, list)
	}
	row := byID[early]
	if row == nil || row["status"] != "failed" {
		t.Fatalf("early failure %v", row)
	}
	if snap, _ := row["snapshot"].(map[string]any); snap == nil || snap["name"] != "Nightly flow" {
		t.Fatalf("snapshot %v", row["snapshot"])
	}
	if row["error"] != "workflow failed before any node started" {
		t.Fatalf("early error %v", row["error"])
	}

	one := c.do(http.MethodGet, "/api/tasks/"+tid+"/workflow-runs?limit=1", "", nil)
	var short []map[string]any
	if err := json.Unmarshal(one.Body.Bytes(), &short); err != nil || len(short) != 1 {
		t.Fatalf("limit %s %v", one.Body.String(), err)
	}

	empty := c.do(http.MethodGet, "/api/tasks/"+sid+"/workflow-runs", "", nil)
	var none []map[string]any
	if empty.Code != 200 {
		t.Fatalf("skill task %d %s", empty.Code, empty.Body.String())
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &none); err != nil || len(none) != 0 {
		t.Fatalf("skill task list %s %v", empty.Body.String(), err)
	}
	if empty.Body.String() == "null" {
		t.Fatal("skill task list was null")
	}

	saved := c.s.deps.Workflows
	c.s.deps.Workflows = nil
	off := c.do(http.MethodGet, "/api/tasks/"+tid+"/workflow-runs", "", nil)
	c.s.deps.Workflows = saved
	var blank []map[string]any
	if off.Code != 200 {
		t.Fatalf("nil workflows %d %s", off.Code, off.Body.String())
	}
	if err := json.Unmarshal(off.Body.Bytes(), &blank); err != nil || len(blank) != 0 {
		t.Fatalf("nil workflows list %s %v", off.Body.String(), err)
	}
}

// The task page fetches workflow runs with the other detail paths, shows them
// on the overview card and the runs tab, and keeps polling while one is active.
func TestTaskWorkflowRunsUI(t *testing.T) {
	js := appJSSource(t)
	for _, want := range []string{
		`base + "/workflow-runs?limit=" + lim`,
		"function taskShowsWfRuns(tk, wfRuns)",
		"function taskWfMini(wfRuns)",
		"function taskWfTable(wfRuns)",
		`data-href="#/workflow-runs/' + enc(x.id) + '"`,
		`href="#/workflow-runs/' + enc(r.id) + '"`,
		`href="#/tasks/' + enc(t.id) + '/runs">`,
		`T("td.wf_runs")`,
		`T("td.node_runs")`,
		"wfRuns.some(function (x) { return x.status === \"running\" || x.status === \"queued\"; })",
		"taskTab(tab, t, items, vers, runs, wfRuns)",
		"taskShowsWfRuns(t, wfRuns)",
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
		for _, k := range []string{"td.wf_runs", "td.node_runs"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
