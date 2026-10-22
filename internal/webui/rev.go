package webui

import (
	"net/http"
	"strconv"
	"time"
)

// handleRev: GET → {"rev": "<changes>.<minute>"}, a cheap "did anything change?" token.
//
// changes is SQLite's total_changes() on the store's only connection: it grows with every
// row any part of tidebot inserts, updates or deletes (runs, usage saves, tasks, settings,
// notifications). minute is the server's Unix minute, so time-based views (next 24 h,
// "today") still refresh once a minute with no writes. Live pages poll this instead of
// re-running their heavy queries, and only refetch when the token moves.
func (s *Server) handleRev(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method")
		return
	}
	var n int64
	if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT total_changes()`).Scan(&n); err != nil {
		writeErr(w, 500, "rev")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"rev": strconv.FormatInt(n, 10) + "." + strconv.FormatInt(time.Now().Unix()/60, 10)})
}
