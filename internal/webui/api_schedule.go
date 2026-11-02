package webui

import (
	"context"

	"net/http"

	"sort"

	"time"

	"github.com/robfig/cron/v3"

	"github.com/Zhengmingming1/tidebot/internal/scheduler"
)

// ---------- schedule ----------

type scheduleItem struct {
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`
	At       string `json:"at"`
	Cron     string `json:"cron"`
	// Skipped: this run was skipped by hand (still listed, dimmed, so it can be put back).
	Skipped bool `json:"skipped,omitempty"`
	// QuietUntil: the run falls in quiet hours and starts when they end (RFC3339 UTC).
	QuietUntil string `json:"quiet_until,omitempty"`
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func (s *Server) upcoming(ctx context.Context, horizon time.Duration, max int) ([]scheduleItem, error) {
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT id, name, cron_expr, skip_runs FROM tasks WHERE status='active' AND cron_expr <> ''`)
	if err != nil {
		return nil, err
	}
	type t3 struct{ id, name, expr, skips string }
	var list []t3
	for rows.Next() {
		var x t3
		if err := rows.Scan(&x.id, &x.name, &x.expr, &x.skips); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, x)
	}
	rows.Close()
	now := time.Now().In(s.loc())
	end := now.Add(horizon)
	out := []scheduleItem{}
	for _, x := range list {
		sch, err := cronParser.Parse(x.expr)
		if err != nil {
			continue
		}
		skipped := map[string]bool{}
		for _, k := range scheduler.ParseSkips(x.skips) {
			skipped[k] = true
		}
		next := now
		for i := 0; i < 6; i++ {
			next = scheduler.NextIn(sch, next, s.loc())
			if next.IsZero() || next.After(end) {
				break
			}
			at := next.UTC().Format(time.RFC3339)
			it := scheduleItem{TaskID: x.id, TaskName: x.name, At: at, Cron: x.expr, Skipped: skipped[at]}
			if s.deps.QuietAt != nil {
				if in, qe := s.deps.QuietAt(next); in {
					it.QuietUntil = qe.UTC().Format(time.RFC3339)
				}
			}
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	items, err := s.upcoming(r.Context(), 7*24*time.Hour, limitParam(r, 20, 100))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "timezone": s.loc().String()})
}

// ---------- overview (home) ----------

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sched, _ := s.upcoming(ctx, 48*time.Hour, 4)
	runs, err := s.queryRuns(ctx, "ORDER BY r.created_at DESC LIMIT 8")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	sum, _ := s.deps.Cost.Summary(ctx, "")
	var maxUSD float64
	for _, b := range sum.Budgets {
		if b.Scope == "global" && b.Period == "day" {
			maxUSD = b.MaxUSD
		}
	}
	// budgets reset at next local midnight of the configured timezone
	reset := s.deps.Cost.ResetsAt()
	var active int
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs WHERE status IN ('queued','running')`).Scan(&active)
	writeJSON(w, 200, map[string]any{
		"buttons":  s.loadButtons(ctx),
		"schedule": sched,
		"runs":     runs,
		"spend": map[string]any{
			"used_usd": sum.TotalUSD, "max_usd": maxUSD, "currency": sum.Currency, "tokens": sum.TotalTokens,
			"by_bucket": sum.ByBucket, "resets_at": reset.UTC().Format(time.RFC3339), "timezone": sum.Timezone,
		},
		"active_runs": active,
		"timezone":    s.loc().String(),
	})
}
