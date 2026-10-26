package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Pausing every schedule at once ("pause all"), for the WebUI, the desktop tray and
// `agentctl call scheduler.pause.set`. A pause is either timed (1 h, 4 h, until 09:00
// tomorrow) or until resumed by hand; it is stored so it survives a restart, and a timed
// pause that ran out while tidebot was off is simply over. Runs started by hand, buttons
// and webhooks are not affected. Tasks that come due during the pause stay due; when
// firing starts again each one follows its own missed-run setting.

const (
	setSchedPause       = "scheduler.pause"          // JSON schedPauseRec; "" = not paused
	setSchedPauseLegacy = "desktop.scheduler_paused" // "true": the tray's old pause (no end)
)

// SchedPauseChoices are the lengths the UI offers, in menu order.
var SchedPauseChoices = []string{"1h", "4h", "tomorrow", "manual"}

type schedPauseRec struct {
	Until string `json:"until,omitempty"` // RFC3339 UTC; "" = until resumed by hand
	Since string `json:"since"`
}

// SchedPauseOption is one menu entry with the end time it would give right now.
type SchedPauseOption struct {
	For   string `json:"for"`
	Until string `json:"until,omitempty"`
}

// SchedPauseView is the pause state the UI shows.
type SchedPauseView struct {
	Paused bool   `json:"paused"`
	Until  string `json:"until,omitempty"` // "" while paused = until resumed by hand
	Since  string `json:"since,omitempty"`
	// Due counts active scheduled tasks whose time has come while paused; they follow
	// their missed-run setting on resume.
	Due     int                `json:"due"`
	Options []SchedPauseOption `json:"options,omitempty"`
}

// schedPauseEnd turns a menu choice into the pause's end ("manual" → zero time).
// "tomorrow" is 09:00 on the next calendar day in the scheduling timezone.
func schedPauseEnd(choice string, now time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	switch choice {
	case "1h":
		return now.Add(time.Hour), nil
	case "4h":
		return now.Add(4 * time.Hour), nil
	case "tomorrow":
		l := now.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day()+1, 9, 0, 0, 0, loc), nil
	case "manual":
		return time.Time{}, nil
	}
	return time.Time{}, errors.New("for must be 1h, 4h, tomorrow or manual")
}

// loadSchedPause applies the stored pause at start-up (and moves the tray's old flag over).
func (a *App) loadSchedPause(ctx context.Context) {
	var rec schedPauseRec
	raw, _ := a.Auth.GetSetting(ctx, setSchedPause)
	if raw == "" {
		if v, _ := a.Auth.GetSetting(ctx, setSchedPauseLegacy); v != "true" {
			return
		}
		_ = a.Auth.SetSetting(ctx, setSchedPauseLegacy, "")
		rec.Since = time.Now().UTC().Format(time.RFC3339)
		if err := a.saveJSONSetting(ctx, setSchedPause, rec); err != nil {
			a.Log.Warn("scheduler pause: migrate", "error", err)
		}
	} else {
		a.loadJSONSetting(ctx, setSchedPause, &rec)
	}
	var until time.Time
	if rec.Until != "" {
		t, err := time.Parse(time.RFC3339, rec.Until)
		if err != nil || !time.Now().Before(t) {
			_ = a.Auth.SetSetting(ctx, setSchedPause, "") // ran out while tidebot was off
			return
		}
		until = t
	}
	a.Sched.PauseUntil(true, until)
	a.Log.Info("scheduler paused (restored)", "until", rec.Until)
}

// onSchedPauseEnded forgets a timed pause the scheduler just ended.
func (a *App) onSchedPauseEnded() {
	if err := a.Auth.SetSetting(context.Background(), setSchedPause, ""); err != nil {
		a.Log.Warn("scheduler pause: clear", "error", err)
	}
}

// SetSchedPause pauses every schedule (choice: 1h | 4h | tomorrow | manual) or resumes
// (paused false; choice ignored). Pausing again replaces the end time.
func (a *App) SetSchedPause(ctx context.Context, paused bool, choice string) (SchedPauseView, error) {
	now := time.Now()
	if !paused {
		a.Sched.SetPaused(false)
		if err := a.Auth.SetSetting(ctx, setSchedPause, ""); err != nil {
			return SchedPauseView{}, err
		}
		a.Log.Info("scheduler resumed")
		return a.SchedPause(ctx), nil
	}
	until, err := schedPauseEnd(choice, now, a.Cfg.Location())
	if err != nil {
		return SchedPauseView{}, err
	}
	rec := schedPauseRec{Since: now.UTC().Format(time.RFC3339)}
	var old schedPauseRec
	if p, _ := a.Sched.PauseState(now); p {
		a.loadJSONSetting(ctx, setSchedPause, &old)
		if old.Since != "" {
			rec.Since = old.Since // extending keeps when it started
		}
	}
	if !until.IsZero() {
		rec.Until = until.UTC().Format(time.RFC3339)
	}
	if err := a.saveJSONSetting(ctx, setSchedPause, rec); err != nil {
		return SchedPauseView{}, err
	}
	a.Sched.PauseUntil(true, until)
	a.Log.Info("scheduler paused", "until", rec.Until)
	return a.SchedPause(ctx), nil
}

// SchedPause is the current state plus the menu's end times.
func (a *App) SchedPause(ctx context.Context) SchedPauseView {
	now := time.Now()
	v := SchedPauseView{}
	if a.Sched == nil {
		return v
	}
	p, until := a.Sched.PauseState(now)
	if p {
		v.Paused = true
		if !until.IsZero() {
			v.Until = until.UTC().Format(time.RFC3339)
		}
		var rec schedPauseRec
		a.loadJSONSetting(ctx, setSchedPause, &rec)
		v.Since = rec.Since
		_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status='active' AND cron_expr != '' AND next_run_at IS NOT NULL AND next_run_at <= ?`,
			now.UTC().Format(time.RFC3339)).Scan(&v.Due)
	}
	for _, c := range SchedPauseChoices {
		o := SchedPauseOption{For: c}
		if t, err := schedPauseEnd(c, now, a.Cfg.Location()); err == nil && !t.IsZero() {
			o.Until = t.UTC().Format(time.RFC3339)
		}
		v.Options = append(v.Options, o)
	}
	return v
}

// SchedPauseBrief is the state without the menu (for status polls).
func (a *App) SchedPauseBrief(ctx context.Context) SchedPauseView {
	v := a.SchedPause(ctx)
	v.Options = nil
	return v
}

func (a *App) schedPauseRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"scheduler.pause.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.SchedPause(ctx), nil },
		// {"paused": true, "for": "1h" | "4h" | "tomorrow" | "manual"} or {"paused": false}
		"scheduler.pause.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Paused bool   `json:"paused"`
				For    string `json:"for"`
			}](raw)
			if err != nil {
				return nil, err
			}
			return a.SetSchedPause(ctx, in.Paused, in.For)
		},
	}
}
