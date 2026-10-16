package webui

import "net/http"

// handleNotifCenter groups recent notifications (failed / needs me / other)
// and lists tasks with pending requirement amendments so the bell can link to them.
func (s *Server) handleNotifCenter(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID        string `json:"id"`
		Event     string `json:"event"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
		Group     string `json:"group"`
	}
	type pend struct {
		TaskID   string `json:"task_id"`
		TaskName string `json:"task_name"`
		Count    int    `json:"count"`
	}
	ctx := r.Context()
	items := []item{}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT id, event, body, created_at FROM notifications ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	for rows.Next() {
		var x item
		if rows.Scan(&x.ID, &x.Event, &x.Body, &x.CreatedAt) == nil {
			switch x.Event {
			case "run_failed", "budget_breached":
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
	writeJSON(w, 200, map[string]any{"items": items, "pending": pending})
}
