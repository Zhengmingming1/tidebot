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
	"strconv"
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

	// Execution hooks provided by app; they start work asynchronously.
	RunTask  func(taskID, bucket string) error
	QuickRun func(prompt, providerName string) error
	LogPath  func(runID string) string
	Location *time.Location
	// QuickRunSkill runs a prompt with a skill materialized (button skill binding).
	QuickRunSkill func(prompt, providerName, skillID, versionID string) error
	Providers     ProviderAdmin

	// Public handlers mounted without session auth (they authenticate themselves),
	// e.g. /hooks/ (webhook token/HMAC) and /metrics (bearer token).
	Public map[string]http.Handler
	// Ext serves POST /api/rpc/<method> for the owner session (plugins, triggers, backup...).
	Ext func(ctx context.Context, method string, raw json.RawMessage) (any, error)
	// ClaudeStatus reports local Claude Code / provider health (cached; force re-checks).
	ClaudeStatus func(ctx context.Context, force bool) any
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

	// OSLanguage is set by the desktop shell; see DesktopHandler.
	OSLanguage string
	// PickFile opens a native file dialog (desktop app only; nil elsewhere).
	PickFile func(title string) (string, error)
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
	for p, h := range s.deps.Public {
		mux.Handle(p, h)
	}
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
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", csp)
		_, _ = w.Write(b)
	})

	s.registerAPI(mux)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		// basic security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

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
	var rl *auth.RateLimitError
	if errors.As(err, &rl) {
		secs := int(rl.RetryAfter.Seconds() + 0.999)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeJSON(w, 429, map[string]any{"error": rl.Error(), "retry_after": secs})
		return
	}
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
		"timezone": s.loc().String(), "version": Version, "language": s.language(r.Context()), "os_language": osLang(r.Context()),
		"currency": s.fx().Display,
	})
}
