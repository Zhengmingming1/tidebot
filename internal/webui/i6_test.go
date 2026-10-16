package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

func loginRaw(c *client, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.RemoteAddr = "10.9.9.9:1234"
	c.s.routes().ServeHTTP(rr, req)
	return rr
}

func TestTwoFAThreeStepFlow(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	code, out := c.json(http.MethodPost, "/api/auth/2fa/begin", nil)
	secret, _ := out["secret"].(string)
	if code != 200 || secret == "" || !strings.HasPrefix(out["qr"].(string), "data:image/png;base64,") {
		t.Fatalf("begin %d %v", code, out)
	}
	// Not enabled until a code verifies.
	if on, _ := c.s.deps.Auth.TOTPEnabled(ctx); on {
		t.Fatal("2fa enabled before verification")
	}
	if code, _ := c.json(http.MethodPost, "/api/auth/2fa/confirm", map[string]any{"code": "000000"}); code != 400 {
		t.Fatalf("bad code accepted: %d", code)
	}
	if on, _ := c.s.deps.Auth.TOTPEnabled(ctx); on {
		t.Fatal("2fa enabled after a wrong code")
	}
	code, out = c.json(http.MethodPost, "/api/auth/2fa/confirm", map[string]any{"code": auth.TOTPCodeAt(secret, time.Now())})
	recs, _ := out["recovery_codes"].([]any)
	if code != 200 || len(recs) != 10 {
		t.Fatalf("confirm %d %v", code, out)
	}
	if on, _ := c.s.deps.Auth.TOTPEnabled(ctx); !on {
		t.Fatal("2fa not enabled after verification")
	}
	// Recovery codes are shown once: status never returns them, a second confirm fails.
	_, st := c.json(http.MethodGet, "/api/auth/2fa/status", nil)
	if st["enabled"] != true || st["recovery_left"].(float64) != 10 || st["recovery_codes"] != nil {
		t.Fatalf("status %v", st)
	}
	if code, _ := c.json(http.MethodPost, "/api/auth/2fa/confirm", map[string]any{"code": auth.TOTPCodeAt(secret, time.Now())}); code != 400 {
		t.Fatalf("second confirm %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/auth/2fa/begin", nil); code != 409 {
		t.Fatalf("begin while enabled %d", code)
	}
	// Login: password alone fails, TOTP works, a recovery code works exactly once.
	if rr := loginRaw(c, `{"password":"testpass99"}`); rr.Code != 401 {
		t.Fatalf("no totp %d", rr.Code)
	}
	if rr := loginRaw(c, `{"password":"testpass99","totp":"`+auth.TOTPCodeAt(secret, time.Now())+`"}`); rr.Code != 200 {
		t.Fatalf("totp login %d", rr.Code)
	}
	rc := recs[0].(string)
	if rr := loginRaw(c, `{"password":"testpass99","totp":"`+strings.ToUpper(rc)+`"}`); rr.Code != 200 {
		t.Fatalf("recovery login %d", rr.Code)
	}
	if rr := loginRaw(c, `{"password":"testpass99","totp":"`+rc+`"}`); rr.Code != 401 {
		t.Fatalf("recovery code reused %d", rr.Code)
	}
	// Disable needs the password.
	if code, _ := c.json(http.MethodPost, "/api/auth/2fa/disable", map[string]any{"password": "nope"}); code != 403 {
		t.Fatalf("disable w/o password %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/auth/2fa/disable", map[string]any{"password": "testpass99"}); code != 200 {
		t.Fatalf("disable %d", code)
	}
}

func TestLoginRateLimitRetryAfter(t *testing.T) {
	c := login(t)
	for i := 0; i < auth.LoginMaxFails; i++ {
		loginRaw(c, `{"password":"wrong-pass"}`)
	}
	rr := loginRaw(c, `{"password":"testpass99"}`)
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" || !strings.Contains(rr.Body.String(), `"retry_after"`) {
		t.Fatalf("want 429 with retry_after, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestNotificationCenterGroups(t *testing.T) {
	c := login(t)
	db := c.s.deps.DB
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO notifications(id,event,body,created_at) VALUES('n1','run_failed','x failed',?),('n2','approval_needed','confirm',?),('n3','run_finished','ok',?)`, now, now, now)
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "nc-task", "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
	_, _ = c.json(http.MethodPost, "/api/tasks/"+tk["id"].(string)+"/amendments", map[string]any{"utterance": "always add a summary"})
	code, out := c.json(http.MethodGet, "/api/notifications/center", nil)
	if code != 200 {
		t.Fatalf("center %d", code)
	}
	groups := map[string]string{}
	for _, x := range out["items"].([]any) {
		m := x.(map[string]any)
		groups[m["id"].(string)] = m["group"].(string)
	}
	if groups["n1"] != "failed" || groups["n2"] != "confirm" || groups["n3"] != "other" {
		t.Fatalf("groups %v", groups)
	}
	pend := out["pending"].([]any)
	if len(pend) != 1 || pend[0].(map[string]any)["task_name"] != "nc-task" {
		t.Fatalf("pending %v", pend)
	}
}

func TestSearchLengthLimit(t *testing.T) {
	c := login(t)
	if code, _ := c.json(http.MethodGet, "/api/search?q="+strings.Repeat("a", 201), nil); code != 400 {
		t.Fatalf("long query accepted: %d", code)
	}
}

func TestClaudeStatusEndpoint(t *testing.T) {
	c := login(t)
	calls := []bool{}
	c.s.deps.ClaudeStatus = func(_ context.Context, force bool) any {
		calls = append(calls, force)
		return map[string]any{"state": "ready"}
	}
	if code, out := c.json(http.MethodGet, "/api/claude/status", nil); code != 200 || out["state"] != "ready" {
		t.Fatalf("get %d %v", code, out)
	}
	c.json(http.MethodPost, "/api/claude/status", nil)
	c.json(http.MethodPost, "/api/claude/status", nil) // within 5s: served from cache
	if len(calls) != 3 || calls[0] || !calls[1] || calls[2] {
		t.Fatalf("force pattern %v", calls)
	}
	if code, _ := c.json(http.MethodDelete, "/api/claude/status", nil); code != 405 {
		t.Fatalf("delete %d", code)
	}
}

func TestNotificationCenterStructured(t *testing.T) {
	c := login(t)
	db := c.s.deps.DB
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO notifications(id,event,body,created_at,msg_key,msg_params) VALUES
		('s1','run_failed','Run failed: x',?,'run_failed','{"name":"<b>x</b>","detail":"boom"}'),
		('s2','run_failed','legacy body',?,'','{}'),
		('s3','run_failed','broken params',?,'run_failed','not json')`, now, now, now)
	_, _ = db.Exec(`INSERT INTO notifications(id,event,body,created_at) VALUES('s4','run_failed','pre-migration style',?)`, now)
	_, out := c.json(http.MethodGet, "/api/notifications/center", nil)
	got := map[string]map[string]any{}
	for _, x := range out["items"].([]any) {
		m := x.(map[string]any)
		got[m["id"].(string)] = m
	}
	p, _ := got["s1"]["params"].(map[string]any)
	if got["s1"]["key"] != "run_failed" || p["name"] != "<b>x</b>" || p["detail"] != "boom" {
		t.Fatalf("structured %v", got["s1"])
	}
	for _, id := range []string{"s2", "s3", "s4"} {
		if k, _ := got[id]["key"].(string); k != "" || got[id]["body"] == "" {
			t.Fatalf("%s should fall back to body: %v", id, got[id])
		}
	}
}
