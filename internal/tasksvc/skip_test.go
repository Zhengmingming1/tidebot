package tasksvc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Skipping one run: the schedule stays on, next_run_at hops over the skipped time, only real
// upcoming fire times can be skipped, undo puts it back, saving the same schedule keeps the
// skips and a new schedule drops them.
func TestSkipRun(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	sched := scheduler.New(store, nil, time.Hour, nil)
	sched.SetLocation(time.UTC)
	svc := New(store, version.New(sqlDB), sched)
	ctx := context.Background()
	const hourly = "0 * * * *"
	tv, err := svc.Create(ctx, CreateInput{Name: "n", Prompt: "p", CronExpr: hourly, Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := tv.Task.ID
	get := func() *db.Task {
		t.Helper()
		tk, err := store.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	h := func(n int) string {
		return time.Now().UTC().Truncate(time.Hour).Add(time.Duration(n) * time.Hour).Format(time.RFC3339)
	}
	if got := get().NextRunAt.String; got != h(1) {
		t.Fatalf("next %s, want %s", got, h(1))
	}

	// default: the next run
	res, err := svc.SkipRun(ctx, id, time.Time{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.At != h(1) || res.NextRunAt != h(2) || len(res.Skipped) != 1 || res.Skipped[0] != h(1) {
		t.Fatalf("skip next: %+v", res)
	}
	if tk := get(); tk.NextRunAt.String != h(2) || tk.Status != "active" {
		t.Fatalf("after skip: %+v", tk)
	}
	// skip the one after too: next hops over both
	at2, _ := time.Parse(time.RFC3339, h(2))
	if res, err = svc.SkipRun(ctx, id, at2, false); err != nil || res.NextRunAt != h(3) || len(res.Skipped) != 2 {
		t.Fatalf("skip 2: %+v %v", res, err)
	}
	// a later one: next stays
	at5, _ := time.Parse(time.RFC3339, h(5))
	if res, err = svc.SkipRun(ctx, id, at5, false); err != nil || res.NextRunAt != h(3) || len(res.Skipped) != 3 {
		t.Fatalf("skip 5: %+v %v", res, err)
	}
	// skipping again is a no-op
	if res, err = svc.SkipRun(ctx, id, at5, false); err != nil || len(res.Skipped) != 3 {
		t.Fatalf("skip twice: %+v %v", res, err)
	}
	// not a fire time, in the past, too far ahead
	if _, err := svc.SkipRun(ctx, id, at2.Add(30*time.Minute), false); !errors.Is(err, ErrSkipNotFireTime) {
		t.Fatalf("half hour: %v", err)
	}
	if _, err := svc.SkipRun(ctx, id, at2.Add(-3*time.Hour), false); !errors.Is(err, ErrSkipNotFireTime) {
		t.Fatalf("past: %v", err)
	}
	if _, err := svc.SkipRun(ctx, id, at2.Add(40*24*time.Hour), false); !errors.Is(err, ErrSkipNotFireTime) {
		t.Fatalf("too far: %v", err)
	}
	// undo the first: it is next again; undo the second: next stays the first
	at1, _ := time.Parse(time.RFC3339, h(1))
	if res, err = svc.SkipRun(ctx, id, at1, true); err != nil || res.NextRunAt != h(1) || len(res.Skipped) != 2 {
		t.Fatalf("undo 1: %+v %v", res, err)
	}
	if res, err = svc.SkipRun(ctx, id, at2, true); err != nil || res.NextRunAt != h(1) || len(res.Skipped) != 1 || res.Skipped[0] != h(5) {
		t.Fatalf("undo 2: %+v %v", res, err)
	}
	// undo of a time that isn't skipped: no-op
	if res, err = svc.SkipRun(ctx, id, at2, true); err != nil || res.NextRunAt != h(1) || len(res.Skipped) != 1 {
		t.Fatalf("undo twice: %+v %v", res, err)
	}

	// the same schedule saved again keeps the skips (and next hops over them)
	if _, err := svc.SkipRun(ctx, id, time.Time{}, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSchedule(ctx, id, hourly); err != nil {
		t.Fatal(err)
	}
	if tk := get(); tk.NextRunAt.String != h(2) || len(scheduler.ParseSkips(tk.SkipRuns)) != 2 {
		t.Fatalf("same schedule: %+v", tk)
	}
	// a snooze that ends keeps hopping over them
	if err := svc.PauseFor(ctx, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStatusWhy(ctx, id, "active", "", ""); err != nil {
		t.Fatal(err)
	}
	if tk := get(); tk.NextRunAt.String != h(2) {
		t.Fatalf("after snooze: %+v", tk)
	}
	// a new schedule drops them
	if err := svc.SetSchedule(ctx, id, "30 * * * *"); err != nil {
		t.Fatal(err)
	}
	if tk := get(); tk.SkipRuns != "" {
		t.Fatalf("new schedule kept skips: %q", tk.SkipRuns)
	}

	// cap
	if err := svc.SetSchedule(ctx, id, hourly); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= scheduler.MaxSkips; i++ {
		at, _ := time.Parse(time.RFC3339, h(i+1))
		if _, err := svc.SkipRun(ctx, id, at, false); err != nil {
			t.Fatalf("skip %d: %v", i, err)
		}
	}
	atX, _ := time.Parse(time.RFC3339, h(scheduler.MaxSkips+2))
	if _, err := svc.SkipRun(ctx, id, atX, false); !errors.Is(err, ErrSkipTooMany) {
		t.Fatalf("cap: %v", err)
	}

	// paused or unscheduled tasks have nothing to skip
	if err := svc.SetStatus(ctx, id, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SkipRun(ctx, id, time.Time{}, false); !errors.Is(err, ErrSkipNoSchedule) {
		t.Fatalf("paused: %v", err)
	}
	manual, _ := svc.Create(ctx, CreateInput{Name: "m", Prompt: "p", Actor: "user"})
	if _, err := svc.SkipRun(ctx, manual.Task.ID, time.Time{}, false); !errors.Is(err, ErrSkipNoSchedule) {
		t.Fatalf("manual: %v", err)
	}
	if _, err := svc.SkipRun(ctx, "task_nope", time.Time{}, false); err == nil {
		t.Fatal("unknown task accepted")
	}
}
