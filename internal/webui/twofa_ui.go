package webui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/auth"

	"rsc.io/qr"
)

// handle2FA: GET status, POST begin|confirm|disable.
// The secret is returned only by "begin" (needed for manual entry); recovery
// codes only by a successful "confirm". Neither is ever logged.
func (s *Server) handle2FA(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.URL.Path, "/api/auth/2fa/")
	ctx := r.Context()
	if op == "status" && r.Method == http.MethodGet {
		on, _ := s.deps.Auth.TOTPEnabled(ctx)
		writeJSON(w, 200, map[string]any{"enabled": on, "recovery_left": s.deps.Auth.RecoveryCodesLeft(ctx)})
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method")
		return
	}
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch op {
	case "begin":
		if on, _ := s.deps.Auth.TOTPEnabled(ctx); on {
			writeErr(w, 409, "2fa already enabled")
			return
		}
		secret, uri, err := s.deps.Auth.BeginTOTP(ctx, "Tidebot", "owner")
		if err != nil {
			writeErr(w, 500, "could not start")
			return
		}
		code, err := qr.Encode(uri, qr.M)
		if err != nil {
			writeErr(w, 500, "qr")
			return
		}
		code.Scale = 6
		png := code.PNG()
		writeJSON(w, 200, map[string]any{"secret": secret, "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(bytes.Clone(png))})
	case "confirm":
		if len(body.Code) > 16 {
			writeErr(w, 400, "code too long")
			return
		}
		codes, err := s.deps.Auth.ConfirmTOTP(ctx, body.Code)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "recovery_codes": codes})
	case "disable":
		if len(body.Password) > 1024 {
			writeErr(w, 400, "too long")
			return
		}
		err := s.deps.Auth.DisableTOTPChecked(ctx, body.Password, auth.ClientIP(r.RemoteAddr))
		var rl *auth.RateLimitError
		switch {
		case errors.As(err, &rl):
			secs := int(rl.RetryAfter.Seconds() + 0.999)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeJSON(w, 429, map[string]any{"error": rl.Error(), "retry_after": secs})
			return
		case errors.Is(err, auth.ErrBadCurrent):
			writeJSON(w, 403, map[string]any{"error": err.Error(), "code": "bad_password"})
			return
		case err != nil:
			writeErr(w, 500, "could not turn off 2FA")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 404, "not found")
	}
}
