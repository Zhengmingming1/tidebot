package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// handleUnlimitedNotice: POST /api/spend/unlimited-notice {"action":"restore"|"dismiss"}.
// The one-time spend page notice about the removed implicit $0.05 per-run caps:
// "restore" puts them all back as they were, "dismiss" keeps them removed.
func (s *Server) handleUnlimitedNotice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	if s.deps.Tasks == nil {
		writeErr(w, 503, "tasks unavailable")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	switch body.Action {
	case "restore":
		n, err := s.deps.Tasks.RestoreUnlimitedDefaults(r.Context(), "webui")
		if errors.Is(err, tasksvc.ErrUnlimitedNoticeClosed) {
			writeErr(w, 409, err.Error())
			return
		}
		if err != nil {
			writeErr(w, 500, "cannot restore the caps")
			return
		}
		writeJSON(w, 200, map[string]any{"restored": n})
	case "dismiss":
		if err := s.deps.Tasks.DismissUnlimitedNotice(r.Context()); err != nil {
			writeErr(w, 500, "cannot save")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 400, "action must be restore or dismiss")
	}
}

// unlimitedNotice is the notice for GET /api/spend (nil = nothing to show).
func (s *Server) unlimitedNotice(r *http.Request) *tasksvc.UnlimitedNotice {
	if s.deps.Tasks == nil {
		return nil
	}
	n, err := s.deps.Tasks.GetUnlimitedNotice(r.Context())
	if err != nil {
		return nil
	}
	return n
}
