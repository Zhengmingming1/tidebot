package webui

import (
	"net/http"
	"sync"
	"time"
)

var lastForce struct {
	sync.Mutex
	at time.Time
}

// handleClaudeStatus: GET returns the cached status; POST re-checks (at most every 5s).
func (s *Server) handleClaudeStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.ClaudeStatus == nil {
		writeErr(w, 404, "not available")
		return
	}
	force := false
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		lastForce.Lock()
		if time.Since(lastForce.at) >= 5*time.Second {
			force, lastForce.at = true, time.Now()
		}
		lastForce.Unlock()
	default:
		writeErr(w, 405, "method")
		return
	}
	writeJSON(w, 200, s.deps.ClaudeStatus(r.Context(), force))
}

// handlePickFile: POST → {"path": "..."} from the native dialog; 501 outside the desktop app.
func (s *Server) handlePickFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	if s.PickFile == nil {
		writeErr(w, 501, "not available in the browser")
		return
	}
	p, err := s.PickFile("Claude Code")
	if err != nil {
		writeErr(w, 500, "dialog failed")
		return
	}
	writeJSON(w, 200, map[string]string{"path": p})
}
