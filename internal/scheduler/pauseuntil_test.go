package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// A timed pause holds due tasks until its end, then ends by itself (once), and firing
// starts again; resuming by hand or pausing again replaces the end.
func TestPauseUntilEndsByItself(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.InsertTask(ctx, db.Task{ID: "task_u", Name: "u", Status: "active", Prompt: "p",
		CronExpr: "*/1 * * * *", OverlapPolicy: "skip", TimeoutMinutes: 5,
		NextRunAt: sql.NullString{String: now.Add(-time.Minute).Format(time.RFC3339), Valid: true},
		CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	var ran, resumed atomic.Int32
	s := New(store, nil, time.Hour, func(context.Context, db.Task) error { ran.Add(1); return nil })
	s.OnAutoResume = func() { resumed.Add(1) }

	end := time.Now().Add(time.Hour)
	s.PauseUntil(true, end)
	if p, u := s.PauseState(time.Now()); !p || !u.Equal(end) || !s.PausedUntil().Equal(end) {
		t.Fatalf("paused=%v until=%v", p, u)
	}
	s.poll(ctx)
	if ran.Load() != 0 || resumed.Load() != 0 {
		t.Fatal("a running timed pause must hold due tasks")
	}
	// Past its end it reads as resumed even before a poll clears it.
	if p, _ := s.PauseState(end.Add(time.Second)); p {
		t.Fatal("pause past its end still reported")
	}
	if s.expirePause(end.Add(-time.Second)) {
		t.Fatal("expired early")
	}
	if !s.expirePause(end.Add(time.Second)) || s.expirePause(end.Add(2*time.Second)) {
		t.Fatal("expirePause must clear exactly once")
	}
	if resumed.Load() != 1 || s.Paused() || !s.PausedUntil().IsZero() {
		t.Fatalf("resumed=%d paused=%v", resumed.Load(), s.Paused())
	}

	// An end already passed: the next poll clears it and fires the due task.
	s.PauseUntil(true, time.Now().Add(-time.Second))
	s.poll(ctx)
	time.Sleep(100 * time.Millisecond)
	if ran.Load() != 1 || resumed.Load() != 2 {
		t.Fatalf("ran=%d resumed=%d after the pause ran out", ran.Load(), resumed.Load())
	}

	// Paused by hand: no end, never cleared by a poll; SetPaused(false) resumes.
	s.SetPaused(true)
	if !s.Paused() || !s.PausedUntil().IsZero() || s.expirePause(time.Now().Add(1000*time.Hour)) {
		t.Fatal("pause by hand must have no end")
	}
	s.PauseUntil(true, time.Now().Add(time.Minute)) // pausing again replaces the end
	if s.PausedUntil().IsZero() {
		t.Fatal("new end not taken")
	}
	s.PauseUntil(false, time.Now().Add(time.Hour)) // resume ignores the time
	if s.Paused() || !s.PausedUntil().IsZero() {
		t.Fatal("resume must clear the pause and its end")
	}
}
