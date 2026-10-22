package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// With every slot busy a new run is queued with a row and a place in line; the user
// can take it out of the line or stop a running one. A stop is quiet: no "run failed"
// notice, no retry, no circuit-breaker strike.
func TestUserStopQueuedAndRunning(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "40")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.5")
	a := newTestApp(t)
	a.Runner = runner.New(a.Store, a.Log, 1)
	ctx := context.Background()
	task, err := a.EnsureTask(ctx, "long", "take your time", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := policy.Default(task.ID)
	p.RetryMax = 3
	if err := a.Policy.Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	events := a.Events.Subscribe(16)
	finished := make(chan struct{})
	go func() {
		a.runWithPolicy(ctx, task, "cron", cost.BucketProduction, 0)
		close(finished)
	}()
	var running string
	waitFor(t, 10*time.Second, func() bool {
		runs, _ := a.Store.ListRuns(ctx, task.ID, 1)
		if len(runs) == 1 && runs[0].Status == "running" {
			running = runs[0].ID
		}
		return running != ""
	})

	queued, err := a.startQuickRunAsync("after you", "")
	if err != nil || queued == "" {
		t.Fatalf("queued start: %q %v", queued, err)
	}
	if pos := a.Runner.QueuePos(queued); pos != 1 {
		t.Fatalf("queue pos %d", pos)
	}
	if was, err := a.StopRun(ctx, queued); err != nil || was != "queued" {
		t.Fatalf("stop queued: %q %v", was, err)
	}
	waitFor(t, 5*time.Second, func() bool {
		st, _, _ := runRow(a, queued)
		return st == "canceled"
	})
	if _, msg, started := runRow(a, queued); msg != db.RunStoppedByUserMsg || started {
		t.Fatalf("queued row: %q started=%v", msg, started)
	}

	if was, err := a.StopRun(ctx, running); err != nil || was != "running" {
		t.Fatalf("stop running: %q %v", was, err)
	}
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("runWithPolicy did not return")
	}
	if st, msg, _ := runRow(a, running); st != "canceled" || msg != db.RunStoppedByUserMsg {
		t.Fatalf("running row: %s %q", st, msg)
	}
	if p := a.Policy.Get(ctx, task.ID); p.ConsecutiveFailures != 0 {
		t.Fatalf("breaker counted the stop: %d", p.ConsecutiveFailures)
	}
	if _, err := a.StopRun(ctx, running); !errors.Is(err, ErrRunNotActive) {
		t.Fatalf("second stop: %v", err)
	}
	if _, err := a.StopRun(ctx, "run_nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	for {
		select {
		case e := <-events:
			if e.Kind == EventRunFailed {
				t.Fatalf("stop reported as failure: %+v", e)
			}
		default:
			runs, _ := a.Store.ListRuns(ctx, task.ID, 5)
			if len(runs) != 1 {
				t.Fatalf("a retry started: %d runs", len(runs))
			}
			return
		}
	}
}

func runRow(a *App, id string) (status, msg string, started bool) {
	_ = a.DB.QueryRow(`SELECT status, COALESCE(error_message,''), started_at IS NOT NULL FROM runs WHERE id=?`, id).Scan(&status, &msg, &started)
	return
}
