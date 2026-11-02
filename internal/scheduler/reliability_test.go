package scheduler

import (
	"context"
	"database/sql"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentDueClaimAndStaleSchedule(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st := db.NewStore(d)
	ctx := context.Background()
	at := time.Now().UTC()
	task := db.Task{ID: "claim", Name: "claim", Status: "active", Prompt: "x", CronExpr: "* * * * *", OverlapPolicy: "parallel", CreatedAt: at.Format(time.RFC3339), UpdatedAt: at.Format(time.RFC3339), NextRunAt: sql.NullString{String: at.Add(-time.Minute).Format(time.RFC3339), Valid: true}}
	if err := st.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := New(st, nil, time.Hour, func(context.Context, db.Task) error { calls.Add(1); return nil })
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.handleDue(ctx, task); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("same fire dispatched %d times", calls.Load())
	}
	if err := s.handleDue(ctx, task); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("stale task dispatched again")
	}
	if _, err := d.Exec(`UPDATE tasks SET next_run_at=?,cron_expr='*/2 * * * *' WHERE id=?`, task.NextRunAt.String, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.handleDue(ctx, task); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("stale poll overwrote edited cron")
	}
}

func TestStopJoinsPollAndAllowsRestart(t *testing.T) {
	s := New(nil, nil, time.Millisecond, nil)
	entered := make(chan struct{}, 1)
	s.Wake = func(ctx context.Context, _ time.Time) { entered <- struct{}{}; <-ctx.Done() }
	s.SetPaused(true)
	for i := 0; i < 5; i++ {
		s.Start(context.Background())
		<-entered
		done := make(chan struct{})
		go func() { s.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Stop did not join poll")
		}
		s.Stop()
	}
}
