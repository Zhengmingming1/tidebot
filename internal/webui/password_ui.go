package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// handlePassword: POST {current, new} changes the owner password from the WebUI.
// Other sessions are signed out, this one stays. Passwords are never logged or echoed.
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method")
		return
	}
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	sess := auth.SessionFromContext(r.Context())
	n, err := s.deps.Auth.ChangePassword(r.Context(), body.Current, body.New, sess.ID, auth.ClientIP(r.RemoteAddr))
	var rl *auth.RateLimitError
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]any{"ok": true, "signed_out": n})
	case errors.As(err, &rl):
		secs := int(rl.RetryAfter.Seconds() + 0.999)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeJSON(w, 429, map[string]any{"error": rl.Error(), "retry_after": secs})
	case errors.Is(err, auth.ErrBadCurrent):
		writeJSON(w, 403, map[string]any{"error": err.Error(), "code": "bad_current"})
	case errors.Is(err, auth.ErrPasswordLen):
		writeJSON(w, 400, map[string]any{"error": err.Error(), "code": "length"})
	case errors.Is(err, auth.ErrSamePassword):
		writeJSON(w, 400, map[string]any{"error": err.Error(), "code": "same"})
	default:
		writeErr(w, 500, "could not change password")
	}
}

// handleSessionsOthers: POST signs out every other browser / device.
func (s *Server) handleSessionsOthers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method")
		return
	}
	n, err := s.deps.Auth.SignOutOthers(r.Context(), auth.SessionFromContext(r.Context()).ID)
	if err != nil {
		writeErr(w, 500, "could not sign out")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "signed_out": n})
}

// handleSessions: GET lists the signed-in browsers and desktop windows (current first).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method")
		return
	}
	list, err := s.deps.Auth.ListSessions(r.Context(), auth.SessionFromContext(r.Context()).ID)
	if err != nil {
		writeErr(w, 500, "could not list sessions")
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": list})
}

// handleSessionSignOut: POST {id} ends one other session by its handle from the list.
func (s *Server) handleSessionSignOut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	err := s.deps.Auth.SignOutSession(r.Context(), body.ID, auth.SessionFromContext(r.Context()).ID)
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]any{"ok": true})
	case errors.Is(err, auth.ErrCurrentSession):
		writeJSON(w, 400, map[string]any{"error": err.Error(), "code": "current"})
	case errors.Is(err, auth.ErrNoSession):
		writeJSON(w, 404, map[string]any{"error": err.Error(), "code": "gone"})
	default:
		writeErr(w, 500, "could not sign out")
	}
}
