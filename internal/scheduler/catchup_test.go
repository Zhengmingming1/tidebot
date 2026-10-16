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

func TestCatchupAndQueue(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	past := now.Add(-3*time.Hour - 30*time.Second).Truncate(time.Hour).Format(time.RFC3339)
	_ = store.InsertTask(ctx, db.Task{ID: "tq", Name: "q", Status: "active", Prompt: "p", CronExpr: "0 * * * *",
		OverlapPolicy: "queue", TimeoutMinutes: 5, NextRunAt: sql.NullString{String: past, Valid: true}, CreatedAt: ts, UpdatedAt: ts})
	var ran atomic.Int32
	var seen int
	s := New(store, nil, time.Hour, func(ctx context.Context, task db.Task) error { ran.Add(1); return nil })
	s.Catchup = func(_ db.Task, missed []time.Time, _ time.Time) int { seen = len(missed); return 2 }
	// active run → queue keeps it due
	_ = store.InsertRun(ctx, db.Run{ID: "r1", TaskID: sql.NullString{String: "tq", Valid: true}, Status: "running", TriggerKind: "cron", Prompt: "p", CreatedAt: ts})
	s.poll(ctx)
	if tk, _ := store.GetTask(ctx, "tq"); ran.Load() != 0 || tk.NextRunAt.String != past {
		t.Fatal("queue must wait without advancing")
	}
	_, _ = sqlDB.Exec(`UPDATE runs SET status='succeeded' WHERE id='r1'`)
	s.poll(ctx)
	if ran.Load() != 2 || seen < 3 {
		t.Fatalf("ran=%d missed=%d", ran.Load(), seen)
	}
}
