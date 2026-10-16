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
