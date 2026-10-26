package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Pause all schedules: stored so it survives a restart (a timed one that ran out while
// tidebot was off is over), the tray's old flag moves over, extending keeps the start,
// tasks that came due are counted, and the WebUI / agentctl reach it over RPC.
func TestSchedPauseState(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	reopen := func() *App {
		t.Helper()
		b, err := Open(a.Cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		return b
	}

	if v := a.SchedPause(ctx); v.Paused || len(v.Options) != 4 || v.Options[3].For != "manual" || v.Options[3].Until != "" || v.Options[0].Until == "" {
		t.Fatalf("initial view %+v", v)
	}
	if _, err := a.SetSchedPause(ctx, true, "2h"); err == nil {
		t.Fatal("unknown length accepted")
	}
	v, err := a.SetSchedPause(ctx, true, "1h")
	if err != nil || !v.Paused || v.Until == "" || v.Since == "" || !a.Sched.Paused() {
		t.Fatalf("pause 1h: %+v %v", v, err)
	}
	until, _ := time.Parse(time.RFC3339, v.Until)
	if d := time.Until(until); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("1h ends in %v", d)
	}
	// Restart: same pause, same end.
	b := reopen()
	if p, u := b.Sched.PauseState(time.Now()); !p || u.UTC().Format(time.RFC3339) != v.Until {
		t.Fatalf("restored %v %v, want until %s", p, u, v.Until)
	}
	// Extending keeps when it started.
	time.Sleep(1100 * time.Millisecond)
	v2, _ := a.SetSchedPause(ctx, true, "manual")
	if v2.Since != v.Since || v2.Until != "" || !a.Sched.Paused() {
		t.Fatalf("extend: %+v (since was %s)", v2, v.Since)
	}

	// Tasks that came due while paused are counted.
	tk, err := a.EnsureTask(ctx, "due one", "x", "*/5 * * * *", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-10 * time.Minute)
	if err := a.Store.UpdateTaskSchedule(ctx, tk.ID, nil, &past); err != nil {
		t.Fatal(err)
	}
	if v := a.SchedPause(ctx); v.Due != 1 {
		t.Fatalf("due = %d", v.Due)
	}
	if st := a.TrayStatus(ctx, nil); !st.Paused || st.Until != "" {
		t.Fatalf("tray %+v", st)
	}

	// RPC: resume, then the brief view drops the menu.
	f := a.extRPC()["scheduler.pause.set"]
	if f == nil || a.extRPC()["scheduler.pause.get"] == nil {
		t.Fatal("rpc not registered")
	}
	out, err := f(ctx, json.RawMessage(`{"paused":false}`))
	if err != nil || out.(SchedPauseView).Paused || a.Sched.Paused() {
		t.Fatalf("resume: %+v %v", out, err)
	}
	if raw, _ := a.Auth.GetSetting(ctx, setSchedPause); raw != "" {
		t.Fatalf("resume left %q", raw)
	}
	if v := a.SchedPauseBrief(ctx); v.Options != nil || v.Due != 0 {
		t.Fatalf("brief %+v", v)
	}
	if b2 := reopen(); b2.Sched.Paused() {
		t.Fatal("resumed pause came back after restart")
	}

	// A timed pause that ran out while tidebot was off is over (and forgotten).
	_ = a.saveJSONSetting(ctx, setSchedPause, schedPauseRec{Until: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), Since: "x"})
	if b3 := reopen(); b3.Sched.Paused() {
		t.Fatal("expired pause restored")
	}
	if raw, _ := a.Auth.GetSetting(ctx, setSchedPause); raw != "" {
		t.Fatalf("expired pause kept: %q", raw)
	}

	// The tray's old flag becomes a pause until resumed by hand.
	_ = a.Auth.SetSetting(ctx, setSchedPauseLegacy, "true")
	b4 := reopen()
	if p, u := b4.Sched.PauseState(time.Now()); !p || !u.IsZero() {
		t.Fatalf("legacy: %v %v", p, u)
	}
	if old, _ := a.Auth.GetSetting(ctx, setSchedPauseLegacy); old != "" {
		t.Fatal("legacy flag not cleared")
	}
	if raw, _ := a.Auth.GetSetting(ctx, setSchedPause); !strings.Contains(raw, "since") {
		t.Fatalf("legacy not migrated: %q", raw)
	}

	// A timed pause ending: the scheduler's hook forgets it.
	_, _ = a.SetSchedPause(ctx, true, "1h")
	a.onSchedPauseEnded()
	if raw, _ := a.Auth.GetSetting(ctx, setSchedPause); raw != "" {
		t.Fatalf("ended pause kept: %q", raw)
	}
}
