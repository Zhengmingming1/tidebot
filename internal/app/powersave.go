package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/power"
)

// Power saving for laptops (final-plan §12 "电源感知"): a Claude Code session keeps a
// CPU core busy for minutes, so on an unplugged laptop scheduled runs of tasks that
// can wait are held back. Each task has a priority (task form): urgent always runs,
// normal waits while the battery is low (≤ 20 % or the OS power saver is on), and
// background only runs on mains power. Settings › General › Power picks how strict
// that is, or turns it off.
//
// Only scheduled runs (cron and their retries) are held: a run you start yourself,
// a button, a webhook or a plugin trigger always starts. A held run ends as skipped
// with the reason (no retry, no failure strike) and the task is remembered; once
// power is back, each remembered task runs once, unless its "missed runs" policy is
// "skip" or the catch-up window has passed (the same rules as a missed schedule).
//
// Idea from Duplicati (Library/Main/Options.cs "disable-on-battery": only scheduled
// backups are skipped, manual ones run; Library/Utility/Power/*: unknown power source
// = proceed). The battery threshold and the per-task tiers are tidebot's own.

const (
	setPowerMode     = "power.mode"
	PowerModeOff     = "off"     // never hold runs back
	PowerModeLow     = "low"     // default: background waits on battery, normal when low
	PowerModeBattery = "battery" // on battery only urgent tasks run
	DefaultPowerMode = PowerModeLow
	lowBatteryPct    = 20
	powerCacheFor    = 15 * time.Second
	powerRecheck     = time.Minute
)

// errPowerSaving: a scheduled run was held back to save battery. The message is
// "power saving: <battery|low|saver> <percent>", shown in the viewer's language.
var errPowerSaving = errors.New("power saving")

type powerWatch struct {
	mu       sync.Mutex
	read     func() power.State // tests swap this
	cached   power.State
	at       time.Time
	deferred map[string]time.Time // task id → first held-back run
	watching bool
	every    time.Duration // tests shrink this
}

func validPowerMode(m string) bool {
	return m == PowerModeOff || m == PowerModeLow || m == PowerModeBattery
}

// PowerMode is the stored mode or the default.
func (a *App) PowerMode(ctx context.Context) string {
	m := ""
	a.loadJSONSetting(ctx, setPowerMode, &m)
	if !validPowerMode(m) {
		return DefaultPowerMode
	}
	return m
}

// powerState is the current reading, cached briefly so a burst of scheduled runs
// (e.g. several tasks at 09:00) reads the battery once. fresh skips the cache.
func (a *App) powerState(fresh bool) power.State {
	w := &a.power
	w.mu.Lock()
	defer w.mu.Unlock()
	if !fresh && !w.at.IsZero() && time.Since(w.at) < powerCacheFor {
		return w.cached
	}
	rd := w.read
	if rd == nil {
		rd = power.Read
	}
	w.cached, w.at = rd(), time.Now()
	return w.cached
}

// powerHold says why a run of this priority must wait ("" = it may start):
// "saver" (OS power saver on), "low" (battery at or under 20 %), "battery".
func powerHold(mode, priority string, st power.State) string {
	if mode == PowerModeOff || priority == "urgent" || !st.Known || !st.OnBattery {
		return ""
	}
	low := st.Saver || (st.Percent >= 0 && st.Percent <= lowBatteryPct)
	why := "battery"
	if st.Saver {
		why = "saver"
	} else if low {
		why = "low"
	}
	switch {
	case priority == "background":
		return why
	case mode == PowerModeBattery:
		return why
	case low:
		return why
	}
	return ""
}

func (a *App) taskPriority(ctx context.Context, taskID string) string {
	if a.Policy == nil {
		return "normal"
	}
	return a.Policy.Get(ctx, taskID).Priority
}

// powerStartCheck runs with the disk check when a run gets its slot.
func (a *App) powerStartCheck(ctx context.Context, taskID, trigger string) error {
	if taskID == "" || (trigger != "cron" && trigger != "retry") {
		return nil
	}
	mode := a.PowerMode(ctx)
	if mode == PowerModeOff {
		return nil
	}
	pr := a.taskPriority(ctx, taskID)
	if pr == "urgent" {
		return nil
	}
	st := a.powerState(false)
	why := powerHold(mode, pr, st)
	if why == "" {
		return nil
	}
	a.Log.Info("run held back to save battery", "task_id", taskID, "why", why, "percent", st.Percent)
	a.deferForPower(taskID, time.Now())
	return fmt.Errorf("%w: %s %d", errPowerSaving, why, st.Percent)
}

// deferForPower remembers the task and starts the (one) watcher, which checks the
// power once a minute and stops as soon as nothing waits: no polling otherwise.
func (a *App) deferForPower(taskID string, at time.Time) {
	w := &a.power
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.deferred == nil {
		w.deferred = map[string]time.Time{}
	}
	if _, ok := w.deferred[taskID]; !ok {
		w.deferred[taskID] = at
	}
	if !w.watching {
		w.watching = true
		go a.watchPower()
	}
}

// PowerWaiting is the number of tasks that will run once power is back.
func (a *App) PowerWaiting() int {
	a.power.mu.Lock()
	defer a.power.mu.Unlock()
	return len(a.power.deferred)
}

func (a *App) watchPower() {
	every := a.power.every
	if every <= 0 {
		every = powerRecheck
	}
	for {
		time.Sleep(every)
		if a.powerTick(context.Background(), time.Now()) == 0 {
			return
		}
	}
}

// powerTick releases the tasks that may run now and returns how many still wait.
// When none wait, the watcher stops (watching is cleared under the same lock).
func (a *App) powerTick(ctx context.Context, now time.Time) int {
	mode := a.PowerMode(ctx)
	st := a.powerState(true)
	w := &a.power
	w.mu.Lock()
	ids := make([]string, 0, len(w.deferred))
	for id := range w.deferred {
		ids = append(ids, id)
	}
	w.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		if powerHold(mode, a.taskPriority(ctx, id), st) != "" {
			continue
		}
		w.mu.Lock()
		first := w.deferred[id]
		delete(w.deferred, id)
		w.mu.Unlock()
		a.catchUpAfterPower(ctx, id, first, now)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	left := len(w.deferred)
	if left == 0 {
		w.watching = false
	}
	return left
}

// catchUpAfterPower runs a held-back task once, by the task's missed-run policy.
func (a *App) catchUpAfterPower(ctx context.Context, taskID string, first, now time.Time) {
	t, err := a.Store.GetTask(ctx, taskID)
	if err != nil || t.Status != "active" {
		return
	}
	if a.Policy != nil {
		p := a.Policy.Get(ctx, taskID)
		if p.Catchup == "skip" || now.Sub(first) > time.Duration(p.CatchupGraceMinutes)*time.Minute {
			a.Log.Info("held-back run not caught up", "task_id", taskID, "catchup", p.Catchup)
			return
		}
	}
	a.Log.Info("power back: running held-back task", "task_id", taskID)
	go a.runWithPolicy(context.Background(), *t, "cron", cost.BucketProduction, 0)
}

// PowerView is Settings › General › Power.
type PowerView struct {
	Mode        string      `json:"mode"`
	DefaultMode string      `json:"default_mode"`
	State       power.State `json:"state"`
	LowPct      int         `json:"low_pct"`
	Waiting     int         `json:"waiting"` // tasks that run once power is back
}

func (a *App) powerView(ctx context.Context) PowerView {
	return PowerView{Mode: a.PowerMode(ctx), DefaultMode: DefaultPowerMode, State: a.powerState(false), LowPct: lowBatteryPct, Waiting: a.PowerWaiting()}
}

func (a *App) powerRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"power.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.powerView(ctx), nil },
		"power.settings": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Mode string `json:"mode"`
			}](raw)
			if err != nil || !validPowerMode(in.Mode) {
				return nil, errors.New("mode must be off, low or battery")
			}
			if err := a.saveJSONSetting(ctx, setPowerMode, in.Mode); err != nil {
				return nil, err
			}
			// A looser mode may let waiting tasks go right away.
			if a.PowerWaiting() > 0 {
				a.powerTick(context.WithoutCancel(ctx), time.Now())
			}
			return a.powerView(ctx), nil
		},
	}
}
