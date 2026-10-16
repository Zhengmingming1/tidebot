package webui

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopHandlerAutoSession(t *testing.T) {
	s, a := testServer(t)
	h, err := s.DesktopHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/me", nil))
	if rr.Code != 200 {
		t.Fatalf("me=%d", rr.Code)
	}
	// writes still need CSRF even in the desktop window
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/buttons", strings.NewReader(`{}`)))
	if rr.Code != 403 {
		t.Fatalf("csrf=%d", rr.Code)
	}
	// after sign-out a fresh local session is issued
	rr = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	h.ServeHTTP(rr, req)
	_, _ = a.DB.ExecContext(context.Background(), `DELETE FROM sessions`)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/me", nil))
	if rr.Code != 200 {
		t.Fatalf("after logout=%d", rr.Code)
	}
}

func TestLoopbackBridgeOneTimeToken(t *testing.T) {
	s, _ := testServer(t)
	b, err := s.StartLoopbackBridge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	base := strings.Split(b.URL, "/__desktop")[0]
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatal(base)
	}
	if r, _ := http.Get(base + "/__desktop?t=wrong"); r.StatusCode != 403 {
		t.Fatalf("bad token=%d", r.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	r, err := c.Get(b.URL)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("bootstrap %v %v", err, r)
	}
	if r, _ := c.Get(base + "/api/me"); r.StatusCode != 200 {
		t.Fatalf("me=%d", r.StatusCode)
	}
	if r, _ := http.Get(b.URL); r.StatusCode != 403 {
		t.Fatalf("token reuse=%d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", base+"/api/me", nil)
	req.Host = "evil.example:80"
	if r, _ := c.Do(req); r.StatusCode != 403 {
		t.Fatalf("host check=%d", r.StatusCode)
	}
}

func TestSetLAN(t *testing.T) {
	s, _ := testServer(t)
	ctx := context.Background()
	s.deps.Cfg.WebUIAddr = "127.0.0.1:0"
	if _, err := s.SetLAN(ctx, true); err == nil {
		t.Fatal("LAN without TLS / allow_http must be refused")
	}
	if s.LANEnabled() {
		t.Fatal("should be off")
	}
	s.deps.Cfg.WebUIAllowHTTP = true
	if _, err := s.SetLAN(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !s.LANEnabled() {
		t.Fatal("should be on")
	}
	if _, err := s.SetLAN(ctx, false); err != nil || s.LANEnabled() {
		t.Fatal("should be off again")
	}
}
