package webui

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

// handleNotifCenter groups recent notifications (failed / needs me / other)
// and lists tasks with pending requirement amendments so the bell can link to them.
func (s *Server) handleNotifCenter(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID        string            `json:"id"`
		Event     string            `json:"event"`
		Body      string            `json:"body"` // server-rendered fallback (legacy rows)
		Key       string            `json:"key,omitempty"`
		Params    map[string]string `json:"params,omitempty"`
		CreatedAt string            `json:"created_at"`
		Group     string            `json:"group"`
	}
	type pend struct {
		TaskID   string `json:"task_id"`
		TaskName string `json:"task_name"`
		Count    int    `json:"count"`
	}
	ctx := r.Context()
	items := []item{}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT id, event, body, created_at, msg_key, msg_params FROM notifications WHERE cleared_at='' ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	for rows.Next() {
		var x item
		var params string
		if rows.Scan(&x.ID, &x.Event, &x.Body, &x.CreatedAt, &x.Key, &params) == nil {
			if x.Key != "" && json.Unmarshal([]byte(params), &x.Params) != nil {
				x.Key = "" // unreadable params: show the stored text instead
			}
			switch x.Event {
			case "run_failed", "budget_breached", "channel_down":
				x.Group = "failed"
			case "approval_needed":
				x.Group = "confirm"
			default:
				x.Group = "other"
			}
			items = append(items, x)
		}
	}
	rows.Close()
	pending := []pend{}
	prow, err := s.deps.DB.QueryContext(ctx, `
SELECT r.task_id, COALESCE(t.name,''), COUNT(1) FROM requirement_items r LEFT JOIN tasks t ON t.id=r.task_id
WHERE r.status='pending' GROUP BY r.task_id ORDER BY MAX(r.created_at) DESC LIMIT 20`)
	if err == nil {
		for prow.Next() {
			var p pend
			if prow.Scan(&p.TaskID, &p.TaskName, &p.Count) == nil {
				pending = append(pending, p)
			}
		}
		prow.Close()
	}
	// enabled notification channels whose last send failed: a dot on Settings › Notifications
	failing := 0
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM notify_channels WHERE enabled=1 AND last_at IS NOT NULL AND last_ok=0`).Scan(&failing)
	writeJSON(w, 200, map[string]any{"items": items, "pending": pending, "channels_failing": failing})
}

var notifIDRe = regexp.MustCompile(`^[\w-]{1,64}$`)

// handleNotifClear hides notifications from the bell (Novu's "archive" / "archive read",
// minus the archive tab): {"ids": [...]} clears those rows, {"read_before": t} clears
// every row created at or before t (the viewer's "seen up to" mark, so whatever this
// browser counts as read), {"undo": batch} brings back the rows of one earlier clear.
// Rows stay in the log; only the center stops listing them.
func (s *Server) handleNotifClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method")
		return
	}
	var body struct {
		IDs        []string `json:"ids"`
		ReadBefore string   `json:"read_before"`
		Undo       string   `json:"undo"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ctx := r.Context()
	if body.Undo != "" {
		if !notifIDRe.MatchString(body.Undo) {
			writeErr(w, 400, "bad batch")
			return
		}
		res, err := s.deps.DB.ExecContext(ctx, `UPDATE notifications SET cleared_at='' WHERE cleared_at=?`, body.Undo)
		if err != nil {
			writeErr(w, 500, "restore failed")
			return
		}
		n, _ := res.RowsAffected()
		writeJSON(w, 200, map[string]any{"restored": n})
		return
	}
	batch := id.New("nc")
	var n int64
	switch {
	case len(body.IDs) > 0:
		if len(body.IDs) > 100 {
			writeErr(w, 400, "too many ids")
			return
		}
		for _, x := range body.IDs {
			if !notifIDRe.MatchString(x) {
				writeErr(w, 400, "bad id")
				return
			}
		}
		tx, err := s.deps.DB.BeginTx(ctx, nil)
		if err != nil {
			writeErr(w, 500, "clear failed")
			return
		}
		for _, x := range body.IDs {
			res, err := tx.ExecContext(ctx, `UPDATE notifications SET cleared_at=? WHERE id=? AND cleared_at=''`, batch, x)
			if err != nil {
				_ = tx.Rollback()
				writeErr(w, 500, "clear failed")
				return
			}
			k, _ := res.RowsAffected()
			n += k
		}
		if tx.Commit() != nil {
			writeErr(w, 500, "clear failed")
			return
		}
	case body.ReadBefore != "":
		t, err := time.Parse(time.RFC3339Nano, body.ReadBefore)
		if err != nil {
			writeErr(w, 400, "bad time")
			return
		}
		// created_at is stored as UTC RFC3339 (seconds), so text order is time order.
		res, err := s.deps.DB.ExecContext(ctx, `UPDATE notifications SET cleared_at=? WHERE cleared_at='' AND created_at<=?`, batch, t.UTC().Format(time.RFC3339))
		if err != nil {
			writeErr(w, 500, "clear failed")
			return
		}
		n, _ = res.RowsAffected()
	default:
		writeErr(w, 400, "nothing to clear")
		return
	}
	out := map[string]any{"cleared": n}
	if n > 0 {
		out["batch"] = batch
	}
	writeJSON(w, 200, out)
}
