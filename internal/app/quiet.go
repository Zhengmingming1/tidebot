package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/scheduler"
)

// Quiet hours for scheduled runs (Settings › General): a daily window, in the scheduling
// timezone, in which scheduled runs don't start; each task that came due runs once when
// the window ends (scheduler/quiet.go). Runs by hand, buttons and webhooks still go.
// Stored as JSON in settings; off by default.

const setSchedQuiet = "scheduler.quiet"

// QuietView is what Settings › General and Home show.
type QuietView struct {
	scheduler.Quiet
	Active    bool   `json:"active"`               // holding scheduled runs right now
	Until     string `json:"until,omitempty"`      // RFC3339 UTC end of the current window
	NextStart string `json:"next_start,omitempty"` // RFC3339 UTC start of the next window
	Due       int    `json:"due"`                  // tasks held now (run once at the end)
	Timezone  string `json:"timezone"`
}

// loadQuiet applies the stored quiet hours at start-up.
func (a *App) loadQuiet(ctx context.Context) {
	q := scheduler.DefaultQuiet
	a.loadJSONSetting(ctx, setSchedQuiet, &q)
	if n, err := q.Normalize(); err == nil {
		q = n
	} else {
		q = scheduler.DefaultQuiet
	}
	a.Sched.SetQuiet(q)
}

// SetQuiet validates, stores and applies quiet hours.
func (a *App) SetQuiet(ctx context.Context, q scheduler.Quiet) (QuietView, error) {
	n, err := q.Normalize()
	if err != nil {
		return QuietView{}, err
	}
	if err := a.saveJSONSetting(ctx, setSchedQuiet, n); err != nil {
		return QuietView{}, err
	}
	a.Sched.SetQuiet(n)
	a.Log.Info("quiet hours saved", "on", n.On, "from", n.From, "to", n.To, "days", n.Days)
	return a.QuietView(ctx), nil
}

// QuietView is the setting plus whether it holds runs now.
func (a *App) QuietView(ctx context.Context) QuietView {
	v := QuietView{Timezone: a.Loc().String()}
	if a.Sched == nil {
		v.Quiet = scheduler.DefaultQuiet
		return v
	}
	now := time.Now()
	v.Quiet = a.Sched.Quiet()
	if v.From == "" {
		v.Quiet = scheduler.DefaultQuiet
	}
	if in, end := a.Sched.QuietState(now); in {
		v.Active, v.Until = true, end.UTC().Format(time.RFC3339)
		_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status='active' AND cron_expr != '' AND next_run_at IS NOT NULL AND next_run_at <= ?`,
			now.UTC().Format(time.RFC3339)).Scan(&v.Due)
	}
	if s := v.Quiet.NextStart(now, a.Loc()); !s.IsZero() {
		v.NextStart = s.UTC().Format(time.RFC3339)
	}
	return v
}

// QuietAt: is t inside quiet hours, and when does that window end (for Home's list).
func (a *App) QuietAt(t time.Time) (bool, time.Time) {
	if a.Sched == nil {
		return false, time.Time{}
	}
	return a.Sched.Quiet().At(t, a.Loc())
}

func (a *App) quietRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"scheduler.quiet.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.QuietView(ctx), nil },
		// {"on": true, "from": "23:00", "to": "07:00", "days": [1,2,3,4,5]} (days: 0 = Sunday; [] = every day)
		"scheduler.quiet.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[scheduler.Quiet](raw)
			if err != nil {
				return nil, err
			}
			return a.SetQuiet(ctx, in)
		},
	}
}
