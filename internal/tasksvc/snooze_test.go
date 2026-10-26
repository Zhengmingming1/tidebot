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

// Pausing one task for a while: it records the end, pausing again changes the end but keeps
// the start, "until resumed" turns it into a plain pause, an automatic pause is left alone,
// and when the end passes the task wakes with the fire times inside the pause skipped.
func TestTaskSnooze(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	sched := scheduler.New(store, nil, time.Hour, nil)
	svc := New(store, version.New(sqlDB), sched)
	ctx := context.Background()
	tv, err := svc.Create(ctx, CreateInput{Name: "n", Prompt: "p", CronExpr: "*/5 * * * *", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := tv.Task.ID
	if err := svc.SetSchedule(ctx, id, "*/5 * * * *"); err != nil {
		t.Fatal(err)
	}
	get := func() *db.Task {
		t.Helper()
		tk, err := store.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}

	if err := svc.PauseFor(ctx, id, time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("end in the past accepted")
	}
	end := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := svc.PauseFor(ctx, id, end); err != nil {
		t.Fatal(err)
	}
	tk := get()
	if tk.Status != "paused" || tk.PauseKind != PauseSnooze || tk.PauseDetail != end.UTC().Format(time.RFC3339) || !tk.PausedAt.Valid {
		t.Fatalf("snooze %+v", tk)
	}
	since := tk.PausedAt.String

	// longer: new end, same start
	end2 := end.Add(3 * time.Hour)
	if err := svc.PauseFor(ctx, id, end2); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.PauseDetail != end2.UTC().Format(time.RFC3339) || tk.PausedAt.String != since {
		t.Fatalf("extend %+v", tk)
	}
	// nothing wakes before the end
	if ids, err := svc.WakeSnoozed(ctx, time.Now()); err != nil || len(ids) != 0 {
		t.Fatalf("woke early %v %v", ids, err)
	}
	// "until resumed": plain pause by hand, never woken
	if err := svc.PauseFor(ctx, id, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.PauseKind != PauseUser || tk.PauseDetail != "" || tk.PausedAt.String != since {
		t.Fatalf("manual %+v", tk)
	}
	if ids, _ := svc.WakeSnoozed(ctx, end2.Add(time.Hour)); len(ids) != 0 {
		t.Fatal("plain pause woken")
	}

	// the end passes: the task wakes, and a due time inside the pause is skipped
	if err := svc.PauseFor(ctx, id, end); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := sqlDB.Exec(`UPDATE tasks SET next_run_at=?, pause_detail=? WHERE id=?`, past, time.Now().Add(-time.Second).UTC().Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	ids, err := svc.WakeSnoozed(ctx, time.Now())
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("wake %v %v", ids, err)
	}
	tk = get()
	if tk.Status != "active" || tk.PauseKind != "" || tk.PausedAt.Valid {
		t.Fatalf("woken %+v", tk)
	}
	nx, _ := time.Parse(time.RFC3339, tk.NextRunAt.String)
	if !nx.After(time.Now()) || nx.After(time.Now().Add(5*time.Minute)) {
		t.Fatalf("next after wake %v", tk.NextRunAt.String)
	}
	if due, _ := store.ListDueTasks(ctx, time.Now()); len(due) != 0 {
		t.Fatal("missed run still due after the snooze")
	}

	// resuming early by hand skips the same way; a fire time still ahead is kept
	if err := svc.PauseFor(ctx, id, end); err != nil {
		t.Fatal(err)
	}
	ahead := tk.NextRunAt.String
	if err := svc.SetStatus(ctx, id, "active", "user"); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.Status != "active" || tk.NextRunAt.String != ahead {
		t.Fatalf("early resume %+v (want next %s)", tk, ahead)
	}

	// a plain pause by hand still catches up on resume (unchanged)
	if err := svc.SetStatus(ctx, id, "paused", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE tasks SET next_run_at=? WHERE id=?`, past, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStatus(ctx, id, "active", "user"); err != nil {
		t.Fatal(err)
	}
	if tk = get(); tk.NextRunAt.String != past {
		t.Fatalf("plain resume moved next_run_at %+v", tk)
	}

	// an automatic pause is not replaced by a snooze; an archived task can't be snoozed
	if err := svc.Pause(ctx, id, PauseBreaker, "network"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PauseFor(ctx, id, end); !errors.Is(err, ErrPausedOther) {
		t.Fatalf("breaker pause replaced: %v", err)
	}
	if tk = get(); tk.PauseKind != PauseBreaker {
		t.Fatalf("kind %q", tk.PauseKind)
	}
	if err := svc.SetStatus(ctx, id, "archived", "x"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PauseFor(ctx, id, end); err == nil {
		t.Fatal("archived task snoozed")
	}
	if err := svc.PauseFor(ctx, "nope", end); err == nil {
		t.Fatal("unknown task snoozed")
	}
}
