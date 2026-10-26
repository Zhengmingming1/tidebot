package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// TestPauseReasonAPI: a paused task says why (kind, detail, time) on the detail, list and
// dashboard answers; resuming from the WebUI clears it and the failure count.
func TestPauseReasonAPI(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	_, a := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Nightly", "prompt": "check", "provider": "anthropic", "cron": "0 2 * * *", "enabled": true})
	id := a["id"].(string)
	_, b := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Budget", "prompt": "check", "provider": "anthropic", "cron": "0 3 * * *", "enabled": true})
	bid := b["id"].(string)

	ps := &policy.Store{DB: c.s.deps.DB}
	for i := 0; i < policy.BreakerThreshold; i++ {
		_, _, _ = ps.RecordOutcome(ctx, id, false)
	}
	if err := c.s.deps.Tasks.Pause(ctx, id, tasksvc.PauseBreaker, "rate_limit"); err != nil {
		t.Fatal(err)
	}
	// Budget pause from the cost service; a later breaker trip doesn't relabel it.
	reason := `task/x: daily USD cap reached (0.50/0.50) <script>`
	if err := c.s.deps.Cost.PauseTask(ctx, bid, reason); err != nil {
		t.Fatal(err)
	}
	_ = c.s.deps.Tasks.Pause(ctx, bid, tasksvc.PauseBreaker, "network")

	_, d := c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["status"] != "paused" || d["pause_kind"] != "breaker" || d["pause_detail"] != "rate_limit" || d["paused_at"] == "" || d["breaker_at"].(float64) != policy.BreakerThreshold {
		t.Fatalf("detail %v", d)
	}
	_, d = c.json(http.MethodGet, "/api/tasks/"+bid, nil)
	if d["pause_kind"] != "budget" || d["pause_detail"] != reason || d["breaker_at"] != nil {
		t.Fatalf("budget detail %v", d)
	}

	type row struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		PauseKind string `json:"pause_kind"`
		PausedAt  string `json:"paused_at"`
		BreakerAt int    `json:"breaker_at"`
	}
	var list []row
	rec := c.do(http.MethodGet, "/api/tasks", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range list {
		kinds[r.ID] = r.PauseKind
	}
	if kinds[id] != "breaker" || kinds[bid] != "budget" {
		t.Fatalf("list kinds %v", kinds)
	}
	var dash struct {
		Tasks []row `json:"tasks"`
	}
	rec = c.do(http.MethodGet, "/api/dashboard", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &dash); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, r := range dash.Tasks {
		if r.ID == id && r.PauseKind == "breaker" && r.PausedAt != "" && r.BreakerAt == policy.BreakerThreshold {
			seen++
		}
		if r.ID == bid && r.PauseKind == "budget" {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("dashboard %+v", dash.Tasks)
	}

	// Resume needs the CSRF token.
	rr := c.do(http.MethodPost, "/api/tasks/"+id+"/resume", "application/json", []byte("{}"))
	if rr.Code != 200 {
		t.Fatalf("resume %d", rr.Code)
	}
	_, d = c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["status"] != "active" || d["pause_kind"] != nil || d["paused_at"] != nil {
		t.Fatalf("after resume %v", d)
	}
	if n := ps.Get(ctx, id).ConsecutiveFailures; n != 0 {
		t.Fatalf("failures after resume %d", n)
	}
	// Pausing by hand from the WebUI is "user".
	c.json(http.MethodPost, "/api/tasks/"+id+"/pause", map[string]any{})
	if _, d = c.json(http.MethodGet, "/api/tasks/"+id, nil); d["pause_kind"] != "user" {
		t.Fatalf("manual pause %v", d)
	}
	// Editing a paused task and keeping it off keeps the reason.
	_, f := c.json(http.MethodGet, "/api/tasks/"+bid+"/form", nil)
	form := f["form"].(map[string]any)
	form["enabled"] = false
	form["name"] = "Budget 2"
	if code, out := c.json(http.MethodPut, "/api/tasks/"+bid, form); code != 200 {
		t.Fatalf("edit %d %v", code, out)
	}
	if _, d = c.json(http.MethodGet, "/api/tasks/"+bid, nil); d["pause_kind"] != "budget" {
		t.Fatalf("edit relabelled the pause %v", d)
	}
}

func TestPauseReasonUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`var PAUSE_KINDS = ["user", "snooze", "breaker", "budget", "pack", "plugin"];`,
		`function taskPill(st, t, small) {`,
		`function pauseNotice(t) {`,
		`taskPill(t.status, t) + idChip(T("td.task_id"), t.id)`,
		`"</button></div></div>" + pauseNotice(t) +`,
		`taskPill("paused", t, true)`,
		`"</td><td>" + taskPill(t.status, t) + `,
		`if (act === "resume" && k === "breaker") toast(T("pz.resumed_reset"));`,
		`'&status=failed">'`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	i := strings.Index(src, "var PAUSE_KINDS = ")
	j := strings.Index(src, "function triggerText(")
	if i < 0 || j < i {
		t.Fatal("pause block not found")
	}
	block := src[i:j]
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "innerHTML"} {
		if strings.Contains(block, bad) {
			t.Errorf("pause block uses %s", bad)
		}
	}
	// Every server-provided value is escaped before it lands in markup.
	for _, want := range []string{`esc(why)`, `esc(body)`, `enc(t.id)`} {
		if !strings.Contains(block, want) {
			t.Errorf("pause block lacks %s", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, want := range []string{".pill.pz-pill {", ".pz-note {", ".pz-note > .link {"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	keys := []string{"pz.pill_breaker", "pz.pill_budget", "pz.pill_pack", "pz.pill_plugin", "pz.why_user", "pz.why_breaker", "pz.why_budget",
		"pz.why_plugin", "pz.why_pack", "pz.note_breaker", "pz.note_budget", "pz.note_plugin", "pz.note_pack", "pz.see_failed", "pz.see_spend",
		"pz.resumed_reset", "pz.resumed_budget", "pz.cls.unknown"}
	// every class policy.Classify can give a failing run
	for _, c := range []string{"budget", "authentication_failed", "billing_error", "invalid_request", "danger_blocked", "acceptance_failed",
		"rate_limit", "overloaded", "server_error", "network", "process_exit"} {
		keys = append(keys, "pz.cls."+c)
	}
	for _, lang := range []string{"zh-CN", "en"} {
		raw, _ := os.ReadFile("static/i18n/" + lang + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", lang, k)
			}
		}
	}
}
