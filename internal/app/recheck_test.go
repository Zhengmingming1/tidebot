package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// queueBehind starts a long cron run of a fresh task on a 1-slot runner and a
// second task's cron run that queues behind it. It returns the running run id,
// the queued task and a channel closed when the queued task's runWithPolicy returns.
func queueBehind(t *testing.T, a *App) (running string, queued db.Task, done chan struct{}) {
	t.Helper()
	ctx := context.Background()
	long, err := a.EnsureTask(ctx, "long", "take your time", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queued, err = a.EnsureTask(ctx, "waits", "after you", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := policy.Default(queued.ID)
	p.RetryMax = 3
	if err := a.Policy.Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	go a.runWithPolicy(ctx, long, "cron", cost.BucketProduction, 0)
	waitFor(t, 10*time.Second, func() bool {
		runs, _ := a.Store.ListRuns(ctx, long.ID, 1)
		if len(runs) == 1 && runs[0].Status == "running" {
			running = runs[0].ID
		}
		return running != ""
	})
	done = make(chan struct{})
	go func() {
		a.runWithPolicy(ctx, queued, "cron", cost.BucketProduction, 0)
		close(done)
	}()
	waitFor(t, 5*time.Second, func() bool {
		runs, _ := a.Store.ListRuns(ctx, queued.ID, 1)
		return len(runs) == 1 && runs[0].Status == "queued"
	})
	return running, queued, done
}

func onlyRun(t *testing.T, a *App, taskID string) string {
	t.Helper()
	runs, _ := a.Store.ListRuns(context.Background(), taskID, 5)
	if len(runs) != 1 {
		t.Fatalf("task %s has %d runs (a retry started?)", taskID, len(runs))
	}
	return runs[0].ID
}

func waitClosed(t *testing.T, ch chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("runWithPolicy did not return")
	}
}

// Runs ahead of a queued run used up the day's budget while it waited: when it
// gets its slot it is checked again and skipped (never started, no "run failed"
// notice, one budget notice, task paused per the budget's action, no retry).
func TestQueuedRunSkippedWhenBudgetRanOut(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "40")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.5")
	a := newTestApp(t)
	a.Runner = runner.New(a.Store, a.Log, 1)
	ctx := context.Background()
	running, waits, done := queueBehind(t, a)
	quick, err := a.startQuickRunAsync("ad hoc, also waiting", "")
	if err != nil || quick == "" {
		t.Fatalf("quick: %q %v", quick, err)
	}
	events := a.Events.Subscribe(32)

	// Spend lands while they wait (e.g. the long run's usage, another core's run).
	if err := a.Cost.RecordEntry(ctx, cost.Entry{RunID: "run_elsewhere", Amount: 1000, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StopRun(ctx, running); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, done)
	waitFor(t, 5*time.Second, func() bool { st, _, _ := runRow(a, quick); return st == "skipped" })

	for _, id := range []string{onlyRun(t, a, waits.ID), quick} {
		st, msg, started := runRow(a, id)
		if st != "skipped" || !strings.HasPrefix(msg, "skipped: budget blocked: ") || started {
			t.Fatalf("%s: %s %q started=%v", id, st, msg, started)
		}
	}
	if tk, _ := a.Store.GetTask(ctx, waits.ID); tk.Status != "paused" {
		t.Fatalf("task not paused by the pause_notify cap: %s", tk.Status)
	}
	time.Sleep(200 * time.Millisecond)
	breaches := 0
	for drained := false; !drained; {
		select {
		case e := <-events:
			switch e.Kind {
			case EventRunFailed:
				t.Fatalf("skip reported as failure: %+v", e)
			case EventBudgetBreached:
				breaches++
			}
		default:
			drained = true
		}
	}
	if breaches != 2 {
		t.Fatalf("budget notices=%d want 2", breaches)
	}
	onlyRun(t, a, waits.ID) // budget errors are not retried
}

// A scheduled run whose task was paused while it waited is skipped at its slot
// and doesn't count against the task; a run the user started by hand still goes.
func TestQueuedCronRunSkippedWhenTaskPaused(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "40")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.5")
	a := newTestApp(t)
	a.Runner = runner.New(a.Store, a.Log, 1)
	ctx := context.Background()
	running, waits, done := queueBehind(t, a)
	manual, err := a.startTaskAsync(waits.ID, cost.BucketProduction)
	if err != nil || manual == "" {
		t.Fatalf("manual: %q %v", manual, err)
	}
	if err := a.Cost.PauseTask(ctx, waits.ID, "user"); err != nil {
		t.Fatal(err)
	}
	events := a.Events.Subscribe(32)
	if _, err := a.StopRun(ctx, running); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, done)

	runs, _ := a.Store.ListRuns(ctx, waits.ID, 5)
	var cronID string
	for _, r := range runs {
		if r.ID != manual {
			cronID = r.ID
		}
	}
	st, msg, started := runRow(a, cronID)
	if st != "skipped" || msg != "skipped: task paused" || started {
		t.Fatalf("cron run: %s %q started=%v", st, msg, started)
	}
	if p := a.Policy.Get(ctx, waits.ID); p.ConsecutiveFailures != 0 {
		t.Fatalf("breaker counted the skip: %d", p.ConsecutiveFailures)
	}
	waitFor(t, 10*time.Second, func() bool { st, _, _ := runRow(a, manual); return st == "running" })
	if _, err := a.StopRun(ctx, manual); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for {
		select {
		case e := <-events:
			if e.Kind == EventRunFailed || e.Kind == EventBudgetBreached {
				t.Fatalf("unexpected notice: %+v", e)
			}
		default:
			if runs, _ := a.Store.ListRuns(ctx, waits.ID, 5); len(runs) != 2 {
				t.Fatalf("runs=%d (a retry started?)", len(runs))
			}
			return
		}
	}
}
