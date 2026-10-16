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

func TestNextRun(t *testing.T) {
	s := New(nil, nil, time.Second, nil)
	from := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	next, err := s.NextRun("*/5 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	if next.Minute() != 5 {
		t.Fatalf("next=%v", next)
	}
}

func TestOverlapSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	task := db.Task{
		ID: "task_x", Name: "t", Status: "active", Prompt: "p",
		CronExpr: "*/1 * * * *", OverlapPolicy: "skip", TimeoutMinutes: 5,
		NextRunAt: sql.NullString{String: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), Valid: true},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// active run blocks
	if err := store.InsertRun(ctx, db.Run{
		ID: "run_active", TaskID: sql.NullString{String: "task_x", Valid: true},
		Status: "running", TriggerKind: "cron", Prompt: "p", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	var ran atomic.Int32
	s := New(store, nil, time.Hour, func(ctx context.Context, task db.Task) error {
		ran.Add(1)
		return nil
	})
	s.poll(ctx)
	if ran.Load() != 0 {
		t.Fatal("should skip while active")
	}
	// clear active
	_ = store.UpdateRunFinish(ctx, db.Run{ID: "run_active", Status: "succeeded"})
	// make due again
	_ = store.UpdateTaskSchedule(ctx, "task_x", nil, ptrTime(time.Now().UTC().Add(-time.Second)))
	s.poll(ctx)
	if ran.Load() != 1 {
		t.Fatalf("ran=%d", ran.Load())
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
