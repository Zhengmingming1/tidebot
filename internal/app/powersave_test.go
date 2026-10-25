package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/power"
)

func fakePower(a *App, st *power.State, mu *sync.Mutex) {
	a.power.every = time.Hour // the test ticks by hand
	a.power.read = func() power.State { mu.Lock(); defer mu.Unlock(); return *st }
}

func countRuns(t *testing.T, a *App, taskID, status string) int {
	t.Helper()
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status=?`, taskID, status).Scan(&n)
	return n
}

func waitRuns(t *testing.T, a *App, taskID, status string, n int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if countRuns(t, a, taskID, status) >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s: want %d %s runs, have %d", taskID, n, status, countRuns(t, a, taskID, status))
}

func setPrio(t *testing.T, a *App, taskID, prio, catchup string) {
	t.Helper()
	p := a.Policy.Get(context.Background(), taskID)
	p.Priority = prio
	if catchup != "" {
		p.Catchup = catchup
	}
	if err := a.Policy.Put(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

// On battery, scheduled runs of tasks that can wait end as skipped with the reason,
// without a retry or a failure strike; runs started by hand and urgent tasks run.
// Plugged in again, each held-back task runs once (unless its catch-up says skip).
func TestPowerSavingHoldsScheduledRuns(t *testing.T) {
	a := newTestApp(t)
	a.retryScale = 0.0001
	ctx := context.Background()
	var mu sync.Mutex
	st := power.State{Known: true, HasBattery: true, OnBattery: true, Percent: 15}
	fakePower(a, &st, &mu)
	mk := func(name string) db.Task {
		tk, err := a.EnsureTask(ctx, name, "do it", "", "", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	normal, urgent, bg, never := mk("normal"), mk("urgent"), mk("bg"), mk("never")
	setPrio(t, a, urgent.ID, "urgent", "")
	setPrio(t, a, bg.ID, "background", "")
	setPrio(t, a, never.ID, "normal", "skip")
	ch := a.Events.Subscribe(64)

	for i := 0; i < 4; i++ { // more than the breaker's 3
		a.runWithPolicy(ctx, normal, "cron", "production", 0)
	}
	var msg string
	_ = a.DB.QueryRow(`SELECT MAX(error_message) FROM runs WHERE task_id=?`, normal.ID).Scan(&msg)
	if n := countRuns(t, a, normal.ID, "skipped"); n != 4 || msg != "skipped: power saving: low 15" {
		t.Fatalf("held runs %d %q", n, msg)
	}
	tk, _ := a.Store.GetTask(ctx, normal.ID)
	if tk.Status != "active" || a.Policy.Get(ctx, normal.ID).ConsecutiveFailures != 0 {
		t.Fatalf("struck: %s %d", tk.Status, a.Policy.Get(ctx, normal.ID).ConsecutiveFailures)
	}
	for len(ch) > 0 {
		if e := <-ch; e.Kind == EventRunFailed || e.Kind == EventBreakerTripped {
			t.Fatalf("event %s", e.Kind)
		}
	}
	// By hand: runs. Urgent: runs.
	if err := a.runTaskTrigger(ctx, normal, "manual", "production"); err != nil {
		t.Fatal(err)
	}
	a.runWithPolicy(ctx, urgent, "cron", "production", 0)
	if countRuns(t, a, normal.ID, "succeeded") != 1 || countRuns(t, a, urgent.ID, "succeeded") != 1 {
		t.Fatal("manual / urgent run held back")
	}
	// 80 %: normal runs, background still waits.
	mu.Lock()
	st.Percent = 80
	mu.Unlock()
	a.power.mu.Lock()
	a.power.at = time.Time{} // drop the cached reading
	a.power.mu.Unlock()
	a.runWithPolicy(ctx, bg, "cron", "production", 0)
	a.runWithPolicy(ctx, never, "cron", "production", 0) // runs at 80 %
	_ = a.DB.QueryRow(`SELECT MAX(error_message) FROM runs WHERE task_id=?`, bg.ID).Scan(&msg)
	if msg != "skipped: power saving: battery 80" || countRuns(t, a, never.ID, "succeeded") != 1 {
		t.Fatalf("at 80%%: bg %q, normal ran %d", msg, countRuns(t, a, never.ID, "succeeded"))
	}
	a.deferForPower(never.ID, time.Now()) // as if held earlier; its catch-up is "skip"
	if a.PowerWaiting() != 3 {
		t.Fatalf("waiting %d", a.PowerWaiting())
	}
	v := a.powerView(ctx)
	if v.Mode != DefaultPowerMode || v.Waiting != 3 || !v.State.OnBattery || v.LowPct != lowBatteryPct {
		t.Fatalf("view %+v", v)
	}
	// Still on battery: nobody leaves except tasks now allowed (normal at 80 %).
	if left := a.powerTick(ctx, time.Now()); left != 1 {
		t.Fatalf("on battery left %d", left)
	}
	waitRuns(t, a, normal.ID, "succeeded", 2) // caught up once
	// Plugged in: background runs once, "skip" doesn't.
	mu.Lock()
	st.OnBattery = false
	mu.Unlock()
	if left := a.powerTick(ctx, time.Now()); left != 0 {
		t.Fatalf("plugged left %d", left)
	}
	waitRuns(t, a, bg.ID, "succeeded", 1)
	time.Sleep(200 * time.Millisecond)
	if countRuns(t, a, never.ID, "succeeded") != 1 || countRuns(t, a, normal.ID, "succeeded") != 2 {
		t.Fatal("caught up a skip-policy task or twice")
	}
	a.power.mu.Lock()
	watching := a.power.watching
	a.power.mu.Unlock()
	if watching {
		t.Fatal("watcher should stop when nothing waits")
	}

	// Outside the catch-up window: forgotten without a run.
	a.deferForPower(bg.ID, time.Now().Add(-7*time.Hour)) // default window 6 h
	if left := a.powerTick(ctx, time.Now()); left != 0 {
		t.Fatal("old deferral kept")
	}
	time.Sleep(200 * time.Millisecond)
	if countRuns(t, a, bg.ID, "succeeded") != 1 {
		t.Fatal("ran past the catch-up window")
	}
}

func TestPowerSettings(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var mu sync.Mutex
	st := power.State{Known: true, HasBattery: true, OnBattery: true, Percent: 50}
	fakePower(a, &st, &mu)
	call := func(in any) (PowerView, error) {
		raw, _ := json.Marshal(in)
		out, err := a.CallExt(ctx, "power.settings", raw)
		if err != nil {
			return PowerView{}, err
		}
		b, _ := json.Marshal(out)
		var v PowerView
		_ = json.Unmarshal(b, &v)
		return v, nil
	}
	if a.PowerMode(ctx) != PowerModeLow {
		t.Fatal("default mode")
	}
	tk, _ := a.EnsureTask(ctx, "t", "do it", "", "", t.TempDir())
	if err := a.powerStartCheck(ctx, tk.ID, "cron"); err != nil {
		t.Fatalf("normal at 50%% in low mode: %v", err)
	}
	if err := a.powerStartCheck(ctx, tk.ID, "webhook"); err != nil {
		t.Fatal("webhook held")
	}
	v, err := call(map[string]any{"mode": "battery"})
	if err != nil || v.Mode != "battery" {
		t.Fatalf("battery %+v %v", v, err)
	}
	if err := a.powerStartCheck(ctx, tk.ID, "retry"); err == nil {
		t.Fatal("retry not held in battery mode")
	}
	if a.PowerWaiting() != 1 {
		t.Fatal("not waiting")
	}
	// Turning it off lets the waiting task go at once.
	v, err = call(map[string]any{"mode": "off"})
	if err != nil || v.Mode != "off" || v.Waiting != 0 {
		t.Fatalf("off %+v %v", v, err)
	}
	waitRuns(t, a, tk.ID, "succeeded", 1)
	for _, bad := range []any{"", "always", 1, nil} {
		if _, err := call(map[string]any{"mode": bad}); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if a.PowerMode(ctx) != "off" {
		t.Fatal("bad value changed the mode")
	}
	_ = a.saveJSONSetting(ctx, setPowerMode, "weird")
	if a.PowerMode(ctx) != DefaultPowerMode {
		t.Fatal("corrupt value should fall back")
	}
	raw, _ := a.CallExt(ctx, "power.get", json.RawMessage(`{}`))
	if pv, ok := raw.(PowerView); !ok || pv.DefaultMode != "low" {
		t.Fatalf("power.get %+v", raw)
	}
}
