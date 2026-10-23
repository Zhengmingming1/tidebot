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
