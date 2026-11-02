package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func TestWorkflowAPIAndOptimisticUpdate(t *testing.T) {
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
	d := workflow.Definition{Name: "API graph", Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", Context: &workflow.ContextPolicy{Mode: "summary", MaxTokens: 512}, RetryMax: 2, RetryBackoffMS: 10, SkillBindings: []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}}}}
	code, out := c.json(http.MethodPost, "/api/workflows", d)
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	wid := out["id"].(string)
	d.Name = "Changed"
	update := map[string]any{"expected_version": 1, "definition": d}
	if code, out = c.json(http.MethodPut, "/api/workflows/"+wid, update); code != 200 {
		t.Fatalf("update %d %v", code, out)
	}
	if code, _ = c.json(http.MethodPut, "/api/workflows/"+wid, update); code != 409 {
		t.Fatalf("lost update allowed: %d", code)
	}
	code, out = c.json(http.MethodPost, "/api/workflows/"+wid+"/run", map[string]string{"input": "from browser", "provider": "kimi-coding"})
	if code != 202 {
		t.Fatalf("run %d %v", code, out)
	}
	rid := out["run_id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		code, out = c.json(http.MethodGet, "/api/workflow-runs/"+rid, nil)
		if out["status"] == "succeeded" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code != 200 || out["status"] != "succeeded" {
		t.Fatalf("run detail %d %v", code, out)
	}
	if code, _ = c.json(http.MethodPost, "/api/workflow-runs/"+rid+"/stop", nil); code != 409 {
		t.Fatalf("finished stop %d", code)
	}
	if code, _ = c.json(http.MethodDelete, "/api/workflows/"+wid, nil); code != 200 {
		t.Fatalf("delete %d", code)
	}
	if code, _ = c.json(http.MethodGet, "/api/workflows/"+wid, nil); code != 404 {
		t.Fatalf("deleted graph visible %d", code)
	}
	if code, _ = c.json(http.MethodGet, "/api/workflow-runs/"+rid, nil); code != 200 {
		t.Fatal("deleting definition lost run history")
	}
	// New routes have the same CSRF and method protections as existing actions.
	req := httptest.NewRequest(http.MethodPost, "/api/workflows", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("missing CSRF accepted %d", rr.Code)
	}
	if code, _ = c.json(http.MethodGet, "/api/workflows/"+wid+"/run", nil); code != 405 {
		t.Fatalf("GET mutation accepted %d", code)
	}
}
