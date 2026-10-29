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

func TestSignedInDevices(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"testpass99"}`))
	req.RemoteAddr = "10.9.9.8:1234"
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0")
	c.s.routes().ServeHTTP(rr, req)
	other := cookieOf(rr)
	if rr.Code != 200 || other == "" {
		t.Fatalf("second login %d", rr.Code)
	}

	resp := c.do(http.MethodGet, "/api/auth/sessions", "", nil)
	if resp.Code != 200 {
		t.Fatalf("list %d", resp.Code)
	}
	if strings.Contains(resp.Body.String(), c.sid) || strings.Contains(resp.Body.String(), other) {
		t.Fatal("session id in the list")
	}
	var out struct {
		Sessions []auth.SessionInfo `json:"sessions"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)
	if len(out.Sessions) != 2 || !out.Sessions[0].Current || out.Sessions[1].Client != "Edge 130" || out.Sessions[1].OS != "Windows" || out.Sessions[1].IP != "10.9.9.8" {
		t.Fatalf("list %+v", out.Sessions)
	}
	_, st := c.json(http.MethodGet, "/api/settings", nil)
	if l, _ := st["sessions"].([]any); len(l) != 2 {
		t.Fatalf("settings sessions %v", st["sessions"])
	}

	// CSRF is required, own window refused, other one signed out, then gone.
	r2 := httptest.NewRequest(http.MethodPost, "/api/auth/sessions/signout", strings.NewReader(`{"id":"`+out.Sessions[1].Handle+`"}`))
	r2.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	w2 := httptest.NewRecorder()
	c.s.routes().ServeHTTP(w2, r2)
	if w2.Code != 403 {
		t.Fatalf("no csrf %d", w2.Code)
	}
	if code, o := c.json(http.MethodPost, "/api/auth/sessions/signout", map[string]any{"id": out.Sessions[0].Handle}); code != 400 || o["code"] != "current" {
		t.Fatalf("current %d %v", code, o)
	}
	if code, _ := c.json(http.MethodPost, "/api/auth/sessions/signout", map[string]any{"id": out.Sessions[1].Handle}); code != 200 {
		t.Fatalf("signout %d", code)
	}
	if _, err := c.s.deps.Auth.GetSession(ctx, other); err == nil {
		t.Fatal("other session still valid")
	}
	if code, o := c.json(http.MethodPost, "/api/auth/sessions/signout", map[string]any{"id": out.Sessions[1].Handle}); code != 404 || o["code"] != "gone" {
		t.Fatalf("gone %d %v", code, o)
	}
	if code, _ := c.json(http.MethodPost, "/api/auth/sessions", nil); code != 405 {
		t.Fatalf("POST list %d", code)
	}
}

func TestDesktopPrunesOldLocalSessions(t *testing.T) {
	s, a := testServer(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ { // three earlier launches of the app
		if _, err := a.CreateLocalSession(ctx, auth.OriginDesktop); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.DesktopHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/settings", nil))
	var st map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &st)
	if rr.Code != 200 || st["other_sessions"].(float64) != 0 {
		t.Fatalf("%d others=%v", rr.Code, st["other_sessions"])
	}
	l, _ := st["sessions"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["kind"] != "desktop" || l[0].(map[string]any)["current"] != true {
		t.Fatalf("sessions %v", st["sessions"])
	}
}

func TestSignedInDevicesUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`'<div id="ses-box">' + sesListHtml(st.sessions) + "</div></section>"`,
		`sesWire($("#set-session"), $("#ses-box"));`,
		`confirmDialog({ title: T("ses.out_title", { n: name }), body: T(b.getAttribute("data-kind") === "desktop" ? "ses.out_body_desktop" : "ses.out_body"), ok: T("ses.out_ok"), danger: true, icon: "logout" })`,
		`api("/api/auth/sessions/signout", { method: "POST", body: { id: id } })`,
		`api("/api/auth/sessions", { fresh: true })`,
		`<strong tabindex="0" data-tip="' + esc(sesTip(x)) + '">' + esc(label) + "</strong>`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	keys := []string{"set.session", "set.session_sub", "ses.list", "ses.desktop", "ses.browser", "ses.local", "ses.since", "ses.from", "ses.until",
		"ses.this_window", "ses.current", "ses.seen", "ses.out", "ses.out_tip", "ses.out_title", "ses.out_body", "ses.out_body_desktop", "ses.out_ok", "ses.out_done", "ses.out_gone"}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
