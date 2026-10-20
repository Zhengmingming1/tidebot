package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func TestDuplicateTask(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	_, src := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Daily Report", "prompt": "summarize", "provider": "anthropic",
		"cron": "0 9 * * *", "enabled": true, "budget_usd": 2.5, "timeout_minutes": 45})
	sid := src["id"].(string)
	// give the source an amended spec (constraint + tool) and a run policy
	cur, _ := c.s.deps.Tasks.Get(ctx, sid)
	sp := *cur.Spec
	sp.Constraints = []tasksvc.SpecConstraint{{ID: "c1", Text: "always add a summary"}}
	sp.AllowedTools = []string{"Read"}
	if _, err := c.s.deps.Tasks.UpdateSpec(ctx, sid, sp, "user", "amend"); err != nil {
		t.Fatal(err)
	}
	ps := &policy.Store{DB: c.s.deps.DB}
	p := ps.Get(ctx, sid)
	p.RetryMax, p.Priority = 0, "urgent"
	if err := ps.Put(ctx, p); err != nil {
		t.Fatal(err)
	}

	code, d := c.json(http.MethodPost, "/api/tasks/"+sid+"/duplicate", map[string]any{"name": "Daily Report 副本"})
	if code != 201 {
		t.Fatalf("duplicate %d %v", code, d)
	}
	did := d["id"].(string)
	if did == sid || d["name"] != "Daily Report 副本" || d["status"] != "paused" || d["cron"] != "0 9 * * *" ||
		d["prompt"] != "summarize" || d["version_no"].(float64) != 1 {
		t.Fatalf("copy %v", d)
	}
	if cs, _ := d["constraints"].([]any); len(cs) != 1 || cs[0] != "always add a summary" {
		t.Fatalf("constraints %v", d["constraints"])
	}
	dv, _ := c.s.deps.Tasks.Get(ctx, did)
	if dv.Spec.TimeoutMinutes != 45 || len(dv.Spec.AllowedTools) != 1 || dv.Task.NextRunAt.String == "" {
		t.Fatalf("spec %+v next %v", dv.Spec, dv.Task.NextRunAt)
	}
	if dp := ps.Get(ctx, did); dp.Priority != "urgent" || dp.RetryMax != 0 {
		t.Fatalf("policy %+v", dp)
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+did+"/form", nil)
	if f := form["form"].(map[string]any); f["budget_usd"].(float64) != 2.5 {
		t.Fatalf("budget %v", f)
	}
	// the source is untouched and stays active
	_, s2 := c.json(http.MethodGet, "/api/tasks/"+sid, nil)
	if s2["status"] != "active" || s2["name"] != "Daily Report" {
		t.Fatalf("source changed %v", s2)
	}

	// same name again → numbered; no name → server default
	_, d2 := c.json(http.MethodPost, "/api/tasks/"+sid+"/duplicate", map[string]any{"name": "Daily Report 副本"})
	if d2["name"] != "Daily Report 副本 2" {
		t.Fatalf("numbered %v", d2["name"])
	}
	rr := c.do(http.MethodPost, "/api/tasks/"+sid+"/duplicate", "application/json", nil)
	if rr.Code != 201 || !strings.Contains(rr.Body.String(), `"Daily Report (copy)"`) {
		t.Fatalf("default name %d %s", rr.Code, rr.Body.String())
	}
	// an archived name is free again
	c.json(http.MethodDelete, "/api/tasks/"+d2["id"].(string), nil)
	if _, d3 := c.json(http.MethodPost, "/api/tasks/"+sid+"/duplicate", map[string]any{"name": "Daily Report 副本"}); d3["name"] != "Daily Report 副本 2" {
		t.Fatalf("after archive %v", d3["name"])
	}

	// errors: unknown / archived source, bad json, no CSRF token
	if code, _ := c.json(http.MethodPost, "/api/tasks/nope/duplicate", nil); code != 404 {
		t.Fatalf("unknown %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/"+d2["id"].(string)+"/duplicate", nil); code != 404 {
		t.Fatalf("archived %d", code)
	}
	if rr := c.do(http.MethodPost, "/api/tasks/"+sid+"/duplicate", "application/json", []byte("{")); rr.Code != 400 {
		t.Fatalf("bad json %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+sid+"/duplicate", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr = httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
}

func TestUniqueNameLongNames(t *testing.T) {
	c := login(t)
	long := strings.Repeat("任", 80)
	c.json(http.MethodPost, "/api/tasks", map[string]any{"name": long, "prompt": "p", "provider": "anthropic"})
	n, err := c.s.deps.Tasks.UniqueName(context.Background(), long, 80)
	if err != nil || len([]rune(n)) != 80 || !strings.HasSuffix(n, " 2") {
		t.Fatalf("%q %v", n, err)
	}
}
