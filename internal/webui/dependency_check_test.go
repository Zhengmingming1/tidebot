package webui

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

func TestDependencyCheckAPI(t *testing.T) {
	c := login(t)
	var seen DependencyCheckRequest
	c.s.deps.StartDependencyCheck = func(req DependencyCheckRequest) (string, error) { seen = req; return "run_dependency", nil }
	c.s.deps.DependencyCheckStatus = func(ctx context.Context, id string) (DependencyCheckStatus, error) {
		if id != "run_dependency" {
			return DependencyCheckStatus{}, sql.ErrNoRows
		}
		return DependencyCheckStatus{RunID: id, ChildRunID: id, Status: "running", StaticFindings: []DependencyFinding{{Dependency: "missing", Kind: "skill", Level: "missing"}}}, nil
	}
	code, out := c.json(http.MethodPost, "/api/dependencies/check", map[string]any{"kind": "draft", "prompt": "analyze, do not run", "skill_bindings": []map[string]string{{"skill_id": "missing", "mode": "follow"}}})
	if code != 202 || out["run_id"] != "run_dependency" || seen.Kind != "draft" || len(seen.SkillBindings) != 1 {
		t.Fatalf("start %d %v %+v", code, out, seen)
	}
	code, out = c.json(http.MethodGet, "/api/dependencies/check/run_dependency", nil)
	if code != 200 || out["status"] != "running" || out["static_findings"] == nil {
		t.Fatalf("status %d %v", code, out)
	}
	if code, _ = c.json(http.MethodGet, "/api/dependencies/check/run_other", nil); code != 404 {
		t.Fatalf("unrelated run: %d", code)
	}
	for _, raw := range []string{`{"kind":"draft","prompt":"x","execute":true}`, `{"kind":"unknown"}`, `{"kind":"workflow_draft"}`, `{"kind":"task","id":"../escape"}`, `{"kind":"draft","prompt":"x"} {}`} {
		rr := c.do(http.MethodPost, "/api/dependencies/check", "application/json", []byte(raw))
		if rr.Code != 400 {
			t.Fatalf("bad input %s: %d", raw, rr.Code)
		}
	}
	// Analysis is a paid run: it uses the same authentication and CSRF boundary as other starts.
	req := httptest.NewRequest(http.MethodPost, "/api/dependencies/check", strings.NewReader(`{"kind":"draft","prompt":"x"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("missing CSRF accepted: %d", rr.Code)
	}
	if rr = c.do(http.MethodDelete, "/api/dependencies/check/run_dependency", "", nil); rr.Code != 405 {
		t.Fatalf("status mutation: %d", rr.Code)
	}
}

func TestDependencyCheckUnavailable(t *testing.T) {
	c := login(t)
	if code, _ := c.json(http.MethodPost, "/api/dependencies/check", map[string]string{"kind": "draft", "prompt": "x"}); code != 503 {
		t.Fatalf("unavailable: %d", code)
	}
}

func TestDependencyPreflightAPIAndRuntimeInput(t *testing.T) {
	c := login(t)
	var seen DependencyCheckRequest
	calls := 0
	c.s.deps.DependencyPreflight = func(ctx context.Context, req DependencyCheckRequest) (DependencyPreflightReport, error) {
		seen = req
		calls++
		return DependencyPreflightReport{RuntimeProfile: DependencyRuntimeProfile{Mode: req.Mode}, ContextUsage: []DependencyContextUsage{}}, nil
	}
	workspace, venv := t.TempDir(), t.TempDir()
	code, out := c.json(http.MethodPost, "/api/dependencies/preflight", map[string]string{"kind": "draft", "prompt": "test", "mode": "read", "workspace": workspace, "python_environment": venv})
	if code != 200 || out["runtime_profile"] == nil || seen.Workspace != workspace || seen.PythonEnvironment != venv || calls != 1 {
		t.Fatalf("preflight %d %v %+v calls=%d", code, out, seen, calls)
	}
	code, _ = c.json(http.MethodGet, "/api/dependencies/preflight?kind=skill&id=skill_example", nil)
	if code != 200 || seen.ID != "skill_example" {
		t.Fatalf("GET preflight %d %+v", code, seen)
	}
	for _, raw := range []string{`{"kind":"draft","prompt":"x","mode":"shell"}`, `{"kind":"draft","prompt":"x","python_environment":"relative"}`, `{"kind":"draft","prompt":"x","execute":true}`, `{"kind":"draft","prompt":"x"} {}`} {
		rr := c.do(http.MethodPost, "/api/dependencies/preflight", "application/json", []byte(raw))
		if rr.Code != 400 {
			t.Fatalf("invalid input accepted %s: %d", raw, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/dependencies/preflight", strings.NewReader(`{"kind":"draft","prompt":"x"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("preflight CSRF not enforced: %d", rr.Code)
	}
}
