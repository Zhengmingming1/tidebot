// Package webui serves the embedded single-user control panel (D6/D8).
package webui

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/amend"
	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Deps are services the HTTP layer needs.
type Deps struct {
	Cfg      *config.Config
	DB       *sql.DB
	Store    *db.Store
	Auth     *auth.Service
	Cost     *cost.Service
	Tasks    *tasksvc.Service
	Amend    *amend.Service
	Skills   *skillstore.Service
	Ver      *version.Service
	Reg      *provider.Registry
	Log      *slog.Logger
	OnToggle func(enabled bool) // optional: persist + apply
}

// Server is a togglable HTTP listener.
type Server struct {
	deps       Deps
	mu         sync.Mutex
	srv        *http.Server
	ln         net.Listener
	enabled    bool
	lastActive time.Time
	sseIdle    time.Duration
}

// New creates a WebUI server (not listening yet).
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	idle := time.Duration(d.Cfg.WebUIIdleMinutes) * time.Minute
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	return &Server{deps: d, sseIdle: idle, lastActive: time.Now()}
}

// Enabled reports current listen state.
func (s *Server) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// Start begins listening if not already up.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled {
		return nil
	}
	addr := s.deps.Cfg.WebUIAddr
	if addr == "" {
		addr = "127.0.0.1:7420"
	}
	if err := s.validateBind(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	handler := s.routes()
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // SSE
	}
	useTLS := s.deps.Cfg.WebUITLSCert != "" && s.deps.Cfg.WebUITLSKey != ""
	s.ln = ln
	s.srv = srv
	s.enabled = true
	s.deps.Log.Info("webui listening", "addr", addr, "tls", useTLS)

	go func() {
		var serveErr error
		if useTLS {
			serveErr = srv.ServeTLS(ln, s.deps.Cfg.WebUITLSCert, s.deps.Cfg.WebUITLSKey)
		} else {
			serveErr = srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.deps.Log.Error("webui serve", "error", serveErr)
		}
		s.mu.Lock()
		s.enabled = false
		s.mu.Unlock()
	}()
	_ = ctx
	return nil
}

// Stop shuts down the listener.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.enabled = false
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}
	err := srv.Shutdown(ctx)
	s.mu.Lock()
	s.srv = nil
	s.ln = nil
	s.mu.Unlock()
	s.deps.Log.Info("webui stopped")
	return err
}

// SetEnabled toggles at runtime without process restart.
func (s *Server) SetEnabled(ctx context.Context, on bool) error {
	if on {
		if err := s.Start(ctx); err != nil {
			return err
		}
	} else {
		if err := s.Stop(ctx); err != nil {
			return err
		}
	}
	if s.deps.OnToggle != nil {
		s.deps.OnToggle(on)
	}
	// persist
	val := "false"
	if on {
		val = "true"
	}
	_ = s.deps.Auth.SetSetting(ctx, "webui.enabled", val)
	s.deps.Cfg.WebUIEnabled = on
	return nil
}

func (s *Server) validateBind(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("webui_addr: %w", err)
	}
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if loopback {
		return nil
	}
	if !s.deps.Cfg.WebUIAllowLAN {
		return fmt.Errorf("refusing non-loopback bind %s (set webui_allow_lan and TLS, or webui_allow_http)", addr)
	}
	useTLS := s.deps.Cfg.WebUITLSCert != "" && s.deps.Cfg.WebUITLSKey != ""
	if !useTLS && !s.deps.Cfg.WebUIAllowHTTP {
		return fmt.Errorf("refusing non-loopback bind without HTTPS (set webui_tls_cert/key or webui_allow_http)")
	}
	_ = tls.VersionTLS12
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(Static, "static")
	if err != nil {
		s.deps.Log.Error("embed static", "error", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := Static.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index missing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write(b)
	})

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.withAuthCSRF(s.handleLogout))
	mux.HandleFunc("/api/me", s.withAuth(s.handleMe))
	mux.HandleFunc("/api/tasks", s.withAuth(s.handleTasks))
	mux.HandleFunc("/api/tasks/", s.withAuth(s.handleTaskSub))
	mux.HandleFunc("/api/amendments/", s.withAuthCSRF(s.handleAmendAction))
	mux.HandleFunc("/api/skills", s.withAuth(s.handleSkills))
	mux.HandleFunc("/api/skills/import", s.withAuthCSRF(s.handleSkillsImport))
	mux.HandleFunc("/api/runs", s.withAuth(s.handleRuns))
	mux.HandleFunc("/api/runs/", s.withAuth(s.handleRunSub))
	mux.HandleFunc("/api/spend", s.withAuth(s.handleSpend))
	mux.HandleFunc("/api/settings", s.withAuth(s.handleSettings))
	mux.HandleFunc("/api/webui", s.withAuthCSRF(s.handleWebUIToggle))
	mux.HandleFunc("/api/budgets", s.withAuthCSRF(s.handleBudgets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		// basic security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) touch() {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

func (s *Server) sseAllowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastActive) < s.sseIdle
}

func (s *Server) withAuth(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth.RequireAuth(s.deps.Auth, http.HandlerFunc(fn)).ServeHTTP(w, r)
	}
}

func (s *Server) withAuthCSRF(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth.RequireAuth(s.deps.Auth, auth.RequireCSRF(http.HandlerFunc(fn))).ServeHTTP(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method")
		return
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ip := auth.ClientIP(r.RemoteAddr)
	sess, err := s.deps.Auth.Login(r.Context(), body.Password, body.TOTP, ip)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		Expires:  sess.ExpiresAt,
	})
	writeJSON(w, 200, map[string]any{"ok": true, "csrf_token": sess.CSRFToken})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		_ = s.deps.Auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := auth.SessionFromContext(r.Context())
	totp, _ := s.deps.Auth.TOTPEnabled(r.Context())
	writeJSON(w, 200, map[string]any{
		"ok": true, "csrf_token": sess.CSRFToken, "totp_enabled": totp, "user": "owner",
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method")
		return
	}
	list, err := s.deps.Tasks.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) handleTaskSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method")
			return
		}
		t, err := s.deps.Tasks.Get(r.Context(), id)
		if err != nil {
			writeErr(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, t)
		return
	}
	switch parts[1] {
	case "amendments":
		if r.Method == http.MethodGet {
			list, err := s.deps.Amend.List(r.Context(), id, "")
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, list)
			return
		}
		if r.Method == http.MethodPost {
			auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Utterance string `json:"utterance"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Utterance == "" {
					writeErr(w, 400, "utterance required")
					return
				}
				a, err := s.deps.Amend.Propose(r.Context(), id, body.Utterance, "user")
				if err != nil {
					writeErr(w, 500, err.Error())
					return
				}
				writeJSON(w, 200, a)
			})).ServeHTTP(w, r)
			return
		}
	case "versions":
		vers, err := s.deps.Ver.ListVersions(r.Context(), version.EntityTask, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		cur, _ := s.deps.Ver.GetPointer(r.Context(), version.EntityTask, id, version.PointerCurrent)
		writeJSON(w, 200, map[string]any{"versions": vers, "current": cur.String})
		return
	case "rollback":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				VersionID string `json:"version_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.VersionID == "" {
				writeErr(w, 400, "version_id required")
				return
			}
			if err := s.deps.Tasks.Rollback(r.Context(), id, body.VersionID, "user", "webui rollback"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		})).ServeHTTP(w, r)
		return
	}
	writeErr(w, 404, "not found")
}

func (s *Server) handleAmendAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/amendments/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		writeErr(w, 404, "not found")
		return
	}
	id, action := parts[0], parts[1]
	switch action {
	case "confirm":
		a, err := s.deps.Amend.Confirm(r.Context(), id, "user")
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, a)
	case "reject":
		if err := s.deps.Amend.Reject(r.Context(), id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "resolve":
		var body struct {
			Resolution string `json:"resolution"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.deps.Amend.ResolveConflict(r.Context(), id, body.Resolution); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 404, "not found")
	}
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Skills.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) handleSkillsImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Dir  string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Dir == "" {
		writeErr(w, 400, "dir required")
		return
	}
	sk, err := s.deps.Skills.ImportDir(r.Context(), body.Name, body.Dir, "user")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, sk)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Store.ListRuns(r.Context(), "", 50)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) handleRunSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "events" {
		writeErr(w, 404, "not found")
		return
	}
	runID := parts[0]
	if !s.sseAllowed() {
		writeErr(w, 429, "sse idle; interact with UI to resume")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no flush")
		return
	}
	fmt.Fprintf(w, "data: run %s stream (placeholder — attach live logs in later phase)\n\n", runID)
	flusher.Flush()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.sseAllowed() {
				fmt.Fprintf(w, "data: [idle stop]\n\n")
				flusher.Flush()
				return
			}
			n++
			fmt.Fprintf(w, "data: heartbeat %d for %s\n\n", n, runID)
			flusher.Flush()
			if n >= 30 {
				return
			}
		}
	}
}

func (s *Server) handleSpend(w http.ResponseWriter, r *http.Request) {
	sum, err := s.deps.Cost.Summary(r.Context(), "")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, sum)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	type prow struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		AuthEnv string `json:"auth_env"`
	}
	var providers []prow
	if s.deps.Reg != nil {
		for _, p := range s.deps.Reg.List() {
			providers = append(providers, prow{Name: p.Name, BaseURL: p.BaseURL, Model: p.Model, AuthEnv: p.AuthEnv})
		}
	}
	sum, _ := s.deps.Cost.Summary(r.Context(), "")
	var globalUSD float64 = 5
	var globalTok int64 = 2_000_000
	for _, b := range sum.Budgets {
		if b.Scope == "global" && b.Period == "day" {
			globalUSD = b.MaxUSD
			globalTok = b.MaxTokens
		}
	}
	writeJSON(w, 200, map[string]any{
		"webui_enabled":     s.Enabled(),
		"providers":         providers,
		"global_max_usd":    globalUSD,
		"global_max_tokens": globalTok,
		"addr":              s.deps.Cfg.WebUIAddr,
	})
}

func (s *Server) handleWebUIToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// Respond before stopping so client gets the reply.
	writeJSON(w, 200, map[string]any{"ok": true, "enabled": body.Enabled})
	if !body.Enabled {
		go func() {
			time.Sleep(200 * time.Millisecond)
			_ = s.SetEnabled(context.Background(), false)
		}()
		return
	}
	_ = s.SetEnabled(r.Context(), true)
}

func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope     string  `json:"scope"`
		ScopeID   string  `json:"scope_id"`
		Period    string  `json:"period"`
		Bucket    string  `json:"bucket"`
		MaxUSD    float64 `json:"max_usd"`
		MaxTokens int64   `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Scope == "" {
		body.Scope = "global"
	}
	if body.Period == "" {
		body.Period = "day"
	}
	b := cost.Budget{
		Scope: body.Scope, ScopeID: body.ScopeID, Period: body.Period, Bucket: body.Bucket,
		MaxUSD:    sql.NullFloat64{Float64: body.MaxUSD, Valid: body.MaxUSD > 0},
		MaxTokens: sql.NullInt64{Int64: body.MaxTokens, Valid: body.MaxTokens > 0},
		Action:    "pause_notify", WarnAtPct: 80,
	}
	if err := s.deps.Cost.UpsertBudget(r.Context(), b); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
