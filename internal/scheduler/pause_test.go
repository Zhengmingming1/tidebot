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

func TestPauseAndGate(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := store.InsertTask(ctx, db.Task{ID: "task_p", Name: "p", Status: "active", Prompt: "p",
		CronExpr: "*/1 * * * *", OverlapPolicy: "skip", TimeoutMinutes: 5,
		NextRunAt: sql.NullString{String: past, Valid: true}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	s := New(store, nil, time.Hour, func(ctx context.Context, task db.Task) error { ran.Add(1); return nil })
	s.SetPaused(true)
	s.poll(ctx)
	if ran.Load() != 0 || !s.Paused() {
		t.Fatal("paused scheduler must not fire")
	}
	s.SetPaused(false)
	s.Gate = func(db.Task) (bool, string) { return true, "on battery" }
	s.poll(ctx)
	if ran.Load() != 0 {
		t.Fatal("gate should defer")
	}
	if tk, _ := store.GetTask(ctx, "task_p"); tk.NextRunAt.String != past {
		t.Fatalf("deferred task must stay due, next=%q", tk.NextRunAt.String)
	}
	s.Gate = nil
	s.poll(ctx)
	time.Sleep(200 * time.Millisecond)
	if ran.Load() != 1 {
		t.Fatalf("ran=%d", ran.Load())
	}
}
