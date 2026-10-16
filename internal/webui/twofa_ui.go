package webui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

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
		if err := s.deps.Auth.DisableTOTPChecked(ctx, body.Password); err != nil {
			writeErr(w, 403, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 404, "not found")
	}
}
