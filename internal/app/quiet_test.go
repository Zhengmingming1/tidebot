package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/scheduler"
)

// Quiet hours: off by default, validated and normalized, stored so they survive a
// restart, applied to the scheduler, reachable over RPC (WebUI / agentctl), shown in the
// "pause all" brief while they hold runs, and Home's list can ask whether a time is held.
func TestQuietHoursSetting(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	v := a.QuietView(ctx)
	if v.On || v.From != "23:00" || v.To != "07:00" || v.Active || v.Timezone == "" {
		t.Fatalf("default view %+v", v)
	}
	for _, bad := range []scheduler.Quiet{{From: "08:00", To: "08:00"}, {From: "25:00", To: "07:00"}, {From: "23:00", To: "07:00", Days: []int{9}}} {
		if _, err := a.SetQuiet(ctx, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	v, err := a.SetQuiet(ctx, scheduler.Quiet{On: true, From: "1:30", To: "06:00", Days: []int{5, 1, 2, 3, 4}})
	if err != nil || !v.On || v.From != "01:30" || len(v.Days) != 5 || v.Days[0] != 1 || v.NextStart == "" {
		t.Fatalf("set: %+v %v", v, err)
	}
	if q := a.Sched.Quiet(); !q.On || q.From != "01:30" {
		t.Fatalf("scheduler got %+v", q)
	}
	// Restart: same setting.
	b, err := Open(a.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if q := b.Sched.Quiet(); !q.On || q.From != "01:30" || q.To != "06:00" || len(q.Days) != 5 {
		t.Fatalf("restored %+v", q)
	}

	// RPC round trip.
	raw, err := a.CallExt(ctx, "scheduler.quiet.set", json.RawMessage(`{"on":false,"from":"22:00","to":"08:00","days":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(raw); !strings.Contains(string(got), `"from":"22:00"`) || !strings.Contains(string(got), `"on":false`) {
		t.Fatalf("set answer %s", got)
	}
	if _, err := a.CallExt(ctx, "scheduler.quiet.set", json.RawMessage(`{"on":true,"from":"x","to":"08:00"}`)); err == nil {
		t.Fatal("bad time over RPC accepted")
	}
	raw, _ = a.CallExt(ctx, "scheduler.quiet.get", nil)
	if got, _ := json.Marshal(raw); !strings.Contains(string(got), `"to":"08:00"`) || !strings.Contains(string(got), `"active":false`) {
		t.Fatalf("get answer %s", got)
	}

	// A window around now holds: the views say so and count due tasks.
	now := time.Now().In(a.Loc())
	f, e := now.Add(-time.Hour).Format("15:04"), now.Add(time.Hour).Format("15:04")
	if _, err := a.SetQuiet(ctx, scheduler.Quiet{On: true, From: f, To: e}); err != nil {
		t.Fatal(err)
	}
	tk, err := a.EnsureTask(ctx, "night job", "x", "*/5 * * * *", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-10 * time.Minute)
	if err := a.Store.UpdateTaskSchedule(ctx, tk.ID, nil, &past); err != nil {
		t.Fatal(err)
	}
	v = a.QuietView(ctx)
	if !v.Active || v.Until == "" || v.Due != 1 {
		t.Fatalf("active view %+v", v)
	}
	sp := a.SchedPauseBrief(ctx)
	if sp.Paused || sp.QuietUntil != v.Until || sp.Due != 1 {
		t.Fatalf("pause brief %+v", sp)
	}
	if in, end := a.QuietAt(time.Now()); !in || end.UTC().Format(time.RFC3339) != v.Until {
		t.Fatalf("QuietAt %v %v", in, end)
	}
	// "Pause all" wins in the brief (it says nothing about quiet hours then).
	if _, err := a.SetSchedPause(ctx, true, "1h"); err != nil {
		t.Fatal(err)
	}
	if sp := a.SchedPauseBrief(ctx); !sp.Paused || sp.QuietUntil != "" {
		t.Fatalf("paused brief %+v", sp)
	}
}

// Runs held back on battery wait through quiet hours too.
func TestQuietHoursHoldPowerCatchup(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now().In(a.Loc())
	if _, err := a.SetQuiet(ctx, scheduler.Quiet{On: true, From: now.Add(-time.Hour).Format("15:04"), To: now.Add(time.Hour).Format("15:04")}); err != nil {
		t.Fatal(err)
	}
	a.power.mu.Lock()
	if a.power.deferred == nil {
		a.power.deferred = map[string]time.Time{}
	}
	a.power.deferred["task_x"] = time.Now().Add(-time.Minute)
	a.power.mu.Unlock()
	if n := a.powerTick(ctx, time.Now()); n != 1 || a.PowerWaiting() != 1 {
		t.Fatalf("quiet hours must keep held-back runs waiting, left=%d", n)
	}
}
