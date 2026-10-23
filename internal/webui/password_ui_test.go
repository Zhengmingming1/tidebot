package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

func TestPasswordChange(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	// A second browser that should be signed out by the change.
	other := loginRaw(c, `{"password":"testpass99"}`)
	if other.Code != 200 {
		t.Fatalf("second login %d", other.Code)
	}
	_, st := c.json(http.MethodGet, "/api/settings", nil)
	if st["password_set"] != true || st["other_sessions"].(float64) != 1 {
		t.Fatalf("settings %v %v", st["password_set"], st["other_sessions"])
	}
	// CSRF is required.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", strings.NewReader(`{"current":"testpass99","new":"brand-new-pass"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("no csrf %d", rr.Code)
	}
	for _, tc := range []struct {
		body map[string]any
		code int
		want string
	}{
		{map[string]any{"current": "nope-nope", "new": "brand-new-pass"}, 403, "bad_current"},
		{map[string]any{"current": "testpass99", "new": "short"}, 400, "length"},
		{map[string]any{"current": "testpass99", "new": strings.Repeat("x", 1025)}, 400, "length"},
		{map[string]any{"current": "testpass99", "new": "testpass99"}, 400, "same"},
	} {
		code, out := c.json(http.MethodPost, "/api/auth/password", tc.body)
		if code != tc.code || out["code"] != tc.want {
			t.Fatalf("%v: %d %v", tc.body["new"], code, out)
		}
		if strings.Contains(strings.ToLower(jsonStr(out)), "testpass99") {
			t.Fatal("password echoed")
		}
	}
	code, out := c.json(http.MethodPost, "/api/auth/password", map[string]any{"current": "testpass99", "new": "brand-new-pass"})
	if code != 200 || out["signed_out"].(float64) != 1 {
		t.Fatalf("change %d %v", code, out)
	}
	// This session keeps working, the other one is gone, the old password no longer logs in.
	if code, _ := c.json(http.MethodGet, "/api/me", nil); code != 200 {
		t.Fatalf("own session dropped: %d", code)
	}
	if _, err := c.s.deps.Auth.GetSession(ctx, cookieOf(other)); err == nil {
		t.Fatal("other session still valid")
	}
	if rr := loginRaw(c, `{"password":"testpass99"}`); rr.Code != 401 {
		t.Fatalf("old password %d", rr.Code)
	}
	if rr := loginRaw(c, `{"password":"brand-new-pass"}`); rr.Code != 200 {
		t.Fatalf("new password %d", rr.Code)
	}
	if code, _ := c.json(http.MethodGet, "/api/auth/password", nil); code != 405 {
		t.Fatalf("GET %d", code)
	}
}

func TestPasswordChangeRateLimited(t *testing.T) {
	c := login(t)
	for i := 0; i < auth.LoginMaxFails; i++ {
		if code, _ := c.json(http.MethodPost, "/api/auth/password", map[string]any{"current": "guess-" + string(rune('a'+i)), "new": "brand-new-pass"}); code != 403 {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	// Wrong guesses from a session count like failed logins: even the right one now waits.
	rr := c.do(http.MethodPost, "/api/auth/password", "application/json", []byte(`{"current":"testpass99","new":"brand-new-pass"}`))
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" || !strings.Contains(rr.Body.String(), `"retry_after"`) {
		t.Fatalf("want 429, got %d %s", rr.Code, rr.Body.String())
	}
	if ok, _ := c.s.deps.Auth.HasPassword(context.Background()); !ok {
		t.Fatal("password lost")
	}
	if rr := loginRaw(c, `{"password":"brand-new-pass"}`); rr.Code == 200 {
		t.Fatal("password changed while rate limited")
	}
}

func TestPasswordFirstSetAndSignOutOthers(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	// Desktop window without a password: the first one needs no current password.
	if _, err := c.s.deps.DB.Exec(`UPDATE users SET password_hash='' WHERE id='owner'`); err != nil {
		t.Fatal(err)
	}
	_, st := c.json(http.MethodGet, "/api/settings", nil)
	if st["password_set"] != false {
		t.Fatalf("password_set %v", st["password_set"])
	}
	if code, out := c.json(http.MethodPost, "/api/auth/password", map[string]any{"new": "first-pass-1"}); code != 200 {
		t.Fatalf("first set %d %v", code, out)
	}
	if ok, _ := c.s.deps.Auth.HasPassword(ctx); !ok {
		t.Fatal("not set")
	}
	a, b := loginRaw(c, `{"password":"first-pass-1"}`), loginRaw(c, `{"password":"first-pass-1"}`)
	if a.Code != 200 || b.Code != 200 {
		t.Fatal("logins")
	}
	code, out := c.json(http.MethodPost, "/api/auth/sessions/others", nil)
	if code != 200 || out["signed_out"].(float64) != 2 {
		t.Fatalf("others %d %v", code, out)
	}
	if n := c.s.deps.Auth.OtherSessions(ctx, c.sid); n != 0 {
		t.Fatalf("left %d", n)
	}
	if code, _ := c.json(http.MethodGet, "/api/me", nil); code != 200 {
		t.Fatal("own session dropped")
	}
}

func TestPasswordUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function pwChange(hasPw) {`,
		`api("/api/auth/password", { method: "POST", body: { current: cur ? cur.value : "", new: nw.value } })`,
		`autocomplete="username" value="owner"`,
		`pw("pw-cur", T("pw.current"), "current-password")`,
		`pw("pw-new", T("pw.new"), "new-password"`,
		`if (x.status === 429) { wait((x.data && x.data.retry_after) || 60); return; }`,
		`api("/api/auth/sessions/others", { method: "POST", body: {} })`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".modal .pw-form { gap: 0; }") {
		t.Error("styles.css lacks .pw-form")
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"pw.change", "pw.set", "pw.unset", "pw.sub", "pw.current", "pw.new", "pw.repeat", "pw.len", "pw.mismatch", "pw.same", "pw.bad_current", "pw.too_many", "pw.save", "pw.done", "pw.done_n", "ses.others", "ses.others_tip", "ses.others_done"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}

func jsonStr(v any) string { b, _ := json.Marshal(v); return string(b) }

func cookieOf(rr *httptest.ResponseRecorder) string {
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == auth.SessionCookie {
			return ck.Value
		}
	}
	return ""
}
