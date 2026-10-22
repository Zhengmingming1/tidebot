package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/cost"
)

type osLangKey struct{}

// osLang is reported by /api/me only for requests from the native window, so that language
// "auto" follows the OS locale there (webviews often report en-US) but not for LAN browsers.
func osLang(ctx context.Context) string {
	v, _ := ctx.Value(osLangKey{}).(string)
	return v
}

// Button is a configured one-click preset (for the desktop tray menu).
type Button struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Buttons lists configured presets.
func (s *Server) Buttons(ctx context.Context) []Button {
	var out []Button
	for _, b := range s.loadButtons(ctx) {
		out = append(out, Button{ID: b.ID, Label: b.Label})
	}
	return out
}

// RunButton starts a preset asynchronously and returns its label.
func (s *Server) RunButton(ctx context.Context, id string) (string, error) {
	label, _, err := s.runButton(ctx, id)
	return label, err
}

// runButton starts a preset and returns its label and the new run's id ("" if queued).
func (s *Server) runButton(ctx context.Context, id string) (label, runID string, err error) {
	for _, b := range s.loadButtons(ctx) {
		if b.ID != id {
			continue
		}
		switch {
		case b.TaskID != "" && s.deps.RunTask != nil:
			runID, err = s.deps.RunTask(b.TaskID, cost.BucketProduction)
		case b.Prompt != "" && b.SkillID != "" && s.deps.QuickRunSkill != nil:
			runID, err = s.deps.QuickRunSkill(expandParams(b.Prompt, b.Params), b.Provider, b.SkillID, b.SkillVerID)
		case b.Prompt != "" && s.deps.QuickRun != nil:
			runID, err = s.deps.QuickRun(expandParams(b.Prompt, b.Params), b.Provider)
		default:
			err = errors.New("execution not available")
		}
		return b.Label, runID, err
	}
	return "", "", errors.New("button not found")
}

// DesktopHandler serves the full WebUI (static + API) for the desktop shell's native window
// through the webview's in-process asset server: no TCP port is opened (D8).
// Every request is authenticated with a local owner session that only this handler knows.
func (s *Server) DesktopHandler(ctx context.Context) (http.Handler, error) {
	var mu sync.Mutex
	sess, err := s.deps.Auth.CreateLocalSession(ctx, "desktop")
	if err != nil {
		return nil, err
	}
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if _, err := s.deps.Auth.GetSession(r.Context(), sess.ID); err != nil {
			// e.g. the user pressed "Sign out" — silently issue a fresh local session.
			if ns, err2 := s.deps.Auth.CreateLocalSession(r.Context(), "desktop"); err2 == nil {
				sess = ns
			}
		}
		id := sess.ID
		mu.Unlock()
		r2 := r.Clone(context.WithValue(r.Context(), osLangKey{}, s.OSLanguage))
		r2.Header.Del("Cookie")
		r2.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: id})
		if r2.RemoteAddr == "" {
			r2.RemoteAddr = "127.0.0.1:0"
		}
		routes.ServeHTTP(w, r2)
	}), nil
}

// LoopbackBridge is the fallback when the webview's asset server can't stream (SSE):
// it listens on 127.0.0.1 at a random port and accepts exactly one bootstrap request
// carrying a one-time token, which is exchanged for an HttpOnly session cookie.
type LoopbackBridge struct {
	URL  string
	stop func()
}

// Close stops the loopback listener.
func (b *LoopbackBridge) Close() {
	if b != nil && b.stop != nil {
		b.stop()
	}
}

// StartLoopbackBridge opens the fallback bridge.
func (s *Server) StartLoopbackBridge(ctx context.Context) (*LoopbackBridge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		ln.Close()
		return nil, err
	}
	token := hex.EncodeToString(raw)
	host := ln.Addr().String()
	var used sync.Once
	var usedFlag bool
	var mu sync.Mutex
	routes := s.routes()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS-rebinding guard: only our exact loopback host is accepted.
		if r.Host != host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/__desktop" {
			got := r.URL.Query().Get("t")
			mu.Lock()
			ok := !usedFlag && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
			if ok {
				used.Do(func() { usedFlag = true })
			}
			mu.Unlock()
			if !ok {
				http.Error(w, "invalid or used token", http.StatusForbidden)
				return
			}
			sess, err := s.deps.Auth.CreateLocalSession(r.Context(), "desktop-loopback")
			if err != nil {
				http.Error(w, "session", http.StatusInternalServerError)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: sess.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: sess.ExpiresAt})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		routes.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &LoopbackBridge{
		URL: fmt.Sprintf("http://%s/__desktop?t=%s", host, token),
		stop: func() {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(c)
		},
	}, nil
}

// LANEnabled reports whether the WebUI currently listens on a non-loopback address.
func (s *Server) LANEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.ln == nil {
		return false
	}
	host, _, _ := net.SplitHostPort(s.ln.Addr().String())
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsLoopback()
}

// SetLAN turns LAN access on (listen on all interfaces, same port) or off (stop listening;
// the desktop window keeps working through the in-process bridge). LAN still requires
// HTTPS or an explicit webui_allow_http, exactly like the server mode.
func (s *Server) SetLAN(ctx context.Context, on bool) (string, error) {
	_, port, err := net.SplitHostPort(s.deps.Cfg.WebUIAddr)
	if err != nil || port == "" {
		port = "7420"
	}
	_ = s.Stop(ctx)
	if !on {
		s.deps.Cfg.WebUIAllowLAN = false
		s.deps.Cfg.WebUIAddr = "127.0.0.1:" + port
		_ = s.deps.Auth.SetSetting(ctx, "webui.lan_enabled", "false")
		return "", nil
	}
	prevAllow, prevAddr := s.deps.Cfg.WebUIAllowLAN, s.deps.Cfg.WebUIAddr
	s.deps.Cfg.WebUIAllowLAN = true
	s.deps.Cfg.WebUIAddr = "0.0.0.0:" + port
	if err := s.Start(ctx); err != nil {
		s.deps.Cfg.WebUIAllowLAN, s.deps.Cfg.WebUIAddr = prevAllow, prevAddr
		return "", err
	}
	if ok, _ := s.deps.Auth.HasPassword(ctx); !ok && s.deps.Log != nil {
		s.deps.Log.Warn("LAN WebUI enabled but no password set; remote logins will fail until `agentctl auth set-password`")
	}
	_ = s.deps.Auth.SetSetting(ctx, "webui.lan_enabled", "true")
	return strings.Replace(s.deps.Cfg.WebUIAddr, "0.0.0.0", "<lan-ip>", 1), nil
}
