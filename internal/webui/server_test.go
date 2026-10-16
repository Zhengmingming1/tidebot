package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/amend"
	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func testServer(t *testing.T) (*Server, *auth.Service) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	store := db.NewStore(sqlDB)
	ver := version.New(sqlDB)
	tasks := tasksvc.New(store, ver, scheduler.New(store, nil, time.Minute, nil))
	am := amend.New(sqlDB, tasks, ver, amend.MockStructurer{})
	skills := skillstore.New(sqlDB, ver, tasks, "")
	authSvc := auth.New(sqlDB)
	_ = authSvc.EnsureOwner(context.Background())
	_ = authSvc.SetPassword(context.Background(), "testpass99")
	costSvc := cost.New(sqlDB, nil)
	_ = costSvc.EnsureDefaults(context.Background())
	cfg := config.Default()
	cfg.WebUIAddr = "127.0.0.1:0"
	cfg.WebUIEnabled = true
	reg, _ := provider.NewRegistry(nil)
	s := New(Deps{
		Cfg: &cfg, DB: sqlDB, Store: store, Auth: authSvc, Cost: costSvc,
		Tasks: tasks, Amend: am, Skills: skills, Ver: ver, Reg: reg,
	})
	return s, authSvc
}

func TestAuthCSRFAndToggle(t *testing.T) {
	s, authSvc := testServer(t)
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop(ctx)
	if !s.Enabled() {
		t.Fatal("expected enabled")
	}

	// login via handler
	body, _ := json.Marshal(map[string]string{"password": "testpass99"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	s.routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login %d %s", rr.Code, rr.Body.String())
	}
	var loginResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &loginResp)
	csrf, _ := loginResp["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("no csrf")
	}
	var sessionID string
	for _, c := range rr.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			sessionID = c.Value
		}
	}
	if sessionID == "" {
		t.Fatal("no session cookie")
	}

	// me
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	s.routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("me %d", rr.Code)
	}

	// CSRF required for POST
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/budgets", bytes.NewReader([]byte(`{"scope":"global","period":"day","max_usd":9}`)))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("want csrf 403 got %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/budgets", bytes.NewReader([]byte(`{"scope":"global","period":"day","max_usd":9}`)))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	req.Header.Set(auth.CSRFHeader, csrf)
	s.routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("budgets %d %s", rr.Code, rr.Body.String())
	}

	// every mutating API route enforces CSRF
	for _, path := range []string{"/api/buttons", "/api/tasks/x/run", "/api/skills/x/rollback", "/api/settings/language", "/api/amendments/x/confirm-batch"} {
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
		s.routes().ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatalf("%s: want csrf 403 got %d", path, rr.Code)
		}
	}

	// language preference persists and is reported by /api/me
	for _, tc := range []struct {
		body string
		code int
	}{{`{"language":"zh-CN"}`, 200}, {`{"language":"fr"}`, 400}} {
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPut, "/api/settings/language", bytes.NewReader([]byte(tc.body)))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
		req.Header.Set(auth.CSRFHeader, csrf)
		s.routes().ServeHTTP(rr, req)
		if rr.Code != tc.code {
			t.Fatalf("language %s: %d", tc.body, rr.Code)
		}
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	s.routes().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"language":"zh-CN"`) {
		t.Fatalf("me language: %s", rr.Body.String())
	}

	// toggle off/on without restart of process
	if err := s.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if s.Enabled() {
		t.Fatal("should be off")
	}
	if err := s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() {
		t.Fatal("should be on")
	}
	_ = authSvc
}

func TestRefuseNonLoopbackWithoutTLS(t *testing.T) {
	s, _ := testServer(t)
	s.deps.Cfg.WebUIAddr = "0.0.0.0:7420"
	s.deps.Cfg.WebUIAllowLAN = true
	s.deps.Cfg.WebUIAllowHTTP = false
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("expected refuse")
	}
}
