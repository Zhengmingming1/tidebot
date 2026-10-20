package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/policy"
)

// Quitting while a scheduled run is going: the run is closed as stopped-on-quit
// right away (not left "running" for the next start), with no "run failed"
// notice, no retry and no circuit-breaker strike.
func TestQuitStopsRunQuietly(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "40")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.5")
	a := newTestApp(t)
	a.Cfg.ShutdownGrace = 100 * time.Millisecond
	ctx := context.Background()
	task, err := a.EnsureTask(ctx, "long", "take your time", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Policy.Put(ctx, policy.Default(task.ID)); err != nil {
		t.Fatal(err)
	}
	events := a.Events.Subscribe(16)
	finished := make(chan struct{})
	go func() {
		a.runWithPolicy(ctx, task, "cron", cost.BucketProduction, 0)
		close(finished)
	}()
	waitFor(t, 10*time.Second, func() bool {
		n, _ := a.Store.CountActiveRuns(ctx)
		return n == 1 && a.Runner.Active() == 1
	})

	start := time.Now()
	a.drainRuns()
	if took := time.Since(start); took > shutdownWriteWait+2*time.Second {
		t.Fatalf("quit took %v", took)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("runWithPolicy did not return")
	}
	runs, err := a.Store.ListRuns(ctx, task.ID, 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
	if runs[0].Status != "canceled" || runs[0].ErrorMessage.String != db.RunStoppedOnQuitMsg {
		t.Fatalf("run %s %q", runs[0].Status, runs[0].ErrorMessage.String)
	}
	if p := a.Policy.Get(ctx, task.ID); p.ConsecutiveFailures != 0 {
		t.Fatalf("breaker counted the quit: %d", p.ConsecutiveFailures)
	}
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case e := <-events:
			if e.Kind == EventRunFailed {
				t.Fatalf("quit reported as failure: %+v", e)
			}
		case <-deadline:
			// New runs (a retry, a webhook, the button) are refused, not started late.
			if _, err := a.RunPrompt(ctx, "late", t.TempDir(), "", "", "manual", 0); err == nil {
				t.Fatal("run started after quit began")
			}
			return
		}
	}
}
