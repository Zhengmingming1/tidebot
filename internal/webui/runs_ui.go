package webui

import (
	"context"
	"net/http"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// rerun: POST /api/runs/<id>/rerun — a task run reruns the task; an ad-hoc run reruns its prompt.
// The answer carries the new run's id (see writeStarted).
func (s *Server) rerun(w http.ResponseWriter, r *http.Request, id string) {
	runs, err := s.queryRuns(r.Context(), "WHERE r.id=?", id)
	if err != nil || len(runs) == 0 {
		writeErr(w, 404, "run not found")
		return
	}
	run := runs[0]
	var newID string
	if run.TaskID != "" {
		if s.deps.RunTask == nil {
			writeErr(w, 503, "runner not available")
			return
		}
		if newID, err = s.deps.RunTask(run.TaskID, cost.BucketProduction); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	} else {
		if s.deps.QuickRun == nil {
			writeErr(w, 503, "runner not available")
			return
		}
		if newID, err = s.deps.QuickRun(run.Prompt, run.Provider); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	s.writeStarted(w, newID)
}

// writeStarted answers a start request: 202 with the new run's id when its row exists
// already, so the UI opens exactly that run. A run waiting for a free slot also gets
// its place in line (queue_pos, 1 = next).
func (s *Server) writeStarted(w http.ResponseWriter, runID string) {
	out := map[string]any{"ok": true}
	if runID != "" {
		out["run_id"] = runID
		if n := s.queuePos(runID); n > 0 {
			out["queue_pos"] = n
		}
	}
	writeJSON(w, 202, out)
}

func (s *Server) queuePos(runID string) int {
	if s.deps.QueuePos == nil {
		return 0
	}
	return s.deps.QueuePos(runID)
}

// stopRun: POST /api/runs/<id>/stop — a queued run leaves the line, a running one is
// killed; both end as canceled ("stopped: by user"). 409 once the run has finished.
func (s *Server) stopRun(w http.ResponseWriter, r *http.Request, id string) {
	if s.deps.StopRun == nil {
		writeErr(w, 503, "runner not available")
		return
	}
	was, err := s.deps.StopRun(id)
	switch {
	case err == nil:
		writeJSON(w, 202, map[string]any{"ok": true, "was": was})
	case was == "":
		writeErr(w, 404, "run not found")
	default:
		writeErr(w, 409, "run is not queued or running")
	}
}

type beat struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	At         string  `json:"at"`
	DurationMS int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd"`
}

// heartbeats returns the last n runs per active/paused task, oldest first.
func (s *Server) heartbeats(ctx context.Context, n int) (map[string][]beat, error) {
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT task_id, id, status, created_at, COALESCE(duration_ms,0), `+s.runAmt("")+` FROM (
		SELECT r.*, ROW_NUMBER() OVER (PARTITION BY r.task_id ORDER BY r.created_at DESC) AS rn
		FROM runs r JOIN tasks t ON t.id = r.task_id WHERE t.status IN ('active','paused')) WHERE rn <= ? ORDER BY created_at ASC`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]beat{}
	for rows.Next() {
		var tid string
		var b beat
		if err := rows.Scan(&tid, &b.ID, &b.Status, &b.At, &b.DurationMS, &b.CostUSD); err != nil {
			return nil, err
		}
		out[tid] = append(out[tid], b)
	}
	return out, rows.Err()
}

// handleDashboard: GET /api/dashboard — running now, next 24h, KPIs, heartbeat bars.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	running, err := s.queryRuns(ctx, "WHERE r.status IN ('queued','running') ORDER BY r.created_at DESC, r.id DESC LIMIT 20")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	next, _ := s.upcoming(ctx, 24*time.Hour, 12)
	beats, err := s.heartbeats(ctx, 20)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type trow struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Beats  []beat `json:"beats"`
		// paused rows: why and when (the pill's word and tooltip)
		PauseKind   string `json:"pause_kind,omitempty"`
		PauseDetail string `json:"pause_detail,omitempty"`
		PausedAt    string `json:"paused_at,omitempty"`
		BreakerAt   int    `json:"breaker_at,omitempty"`
	}
	tasks := []trow{}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT id, name, status, pause_kind, pause_detail, COALESCE(paused_at, '') FROM tasks WHERE status IN ('active','paused') ORDER BY name`)
	if err == nil {
		for rows.Next() {
			var t trow
			if rows.Scan(&t.ID, &t.Name, &t.Status, &t.PauseKind, &t.PauseDetail, &t.PausedAt) == nil {
				if t.Status != "paused" {
					t.PauseKind, t.PauseDetail, t.PausedAt = "", "", ""
				} else if t.PauseKind == tasksvc.PauseBreaker {
					t.BreakerAt = policy.BreakerThreshold
				}
				t.Beats = beats[t.ID]
				if t.Beats == nil {
					t.Beats = []beat{}
				}
				tasks = append(tasks, t)
			}
		}
		rows.Close()
	}
	start := time.Now().In(s.loc())
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, s.loc()).UTC().Format(time.RFC3339)
	var total, ok int
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0) FROM runs WHERE created_at >= ?`, day).Scan(&total, &ok)
	sum, _ := s.deps.Cost.Summary(ctx, "")
	var maxUSD float64
	for _, b := range sum.Budgets {
		if b.Scope == "global" && b.Period == "day" {
			maxUSD = b.MaxUSD
		}
	}
	writeJSON(w, 200, map[string]any{
		"running": running, "next": next, "tasks": tasks,
		"kpi": map[string]any{"runs_today": total, "succeeded_today": ok, "spent_today": sum.TotalUSD, "budget_usd": maxUSD},
	})
}
