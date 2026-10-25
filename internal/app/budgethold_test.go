package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// Two runs going at once used to each get the whole remainder as their cap. Now
// a running run holds its cap: a run starting next to it with less than a cent
// left is skipped (run row with the reason, no pause, no breaker strike, no
// retry, no "budget breached" notice) and runs normally once the first one ends.
func TestConcurrentRunsShareBudget(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "40")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.5")
	a := newTestApp(t)
	a.Runner = runner.New(a.Store, a.Log, 2)
	ctx := context.Background()
	if calculator(mustProfile(t, a, "deepseek")).Billing.Mode != pricing.ModeMetered {
		t.Skip("deepseek preset is not metered")
	}
	f := a.cachedFX()
	if err := a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "global", Period: "day", Action: "pause_notify", WarnAtPct: 100,
		MaxUSD: sql.NullFloat64{Float64: f.Convert(0.055, pricing.USD, f.Display), Valid: true}, Currency: f.Display}); err != nil {
		t.Fatal(err)
	}
	long, err := a.EnsureTask(ctx, "long", "take your time", "", "deepseek", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.EnsureTask(ctx, "next", "me too", "", "deepseek", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	go a.runWithPolicy(ctx, long, "cron", cost.BucketProduction, 0)
	var running string
	waitFor(t, 10*time.Second, func() bool {
		runs, _ := a.Store.ListRuns(ctx, long.ID, 1)
		if len(runs) == 1 && runs[0].Status == "running" {
			running = runs[0].ID
		}
		return running != ""
	})
	if held, n := a.Cost.Held("global", "", "", pricing.USD); n != 1 || held < 0.04 || held > 0.0500001 {
		t.Fatalf("first run holds %v (%d)", held, n)
	}
	events := a.Events.Subscribe(32)
	done := make(chan struct{})
	go func() { a.runWithPolicy(ctx, next, "cron", cost.BucketProduction, 0); close(done) }()
	waitClosed(t, done)
	id := onlyRun(t, a, next.ID)
	st, msg, started := runRow(a, id)
	if st != "skipped" || started || !strings.HasPrefix(msg, "skipped: budget blocked: global/ daily ") || !strings.Contains(msg, " cap held by 1 running (") {
		t.Fatalf("second run: %s %q started=%v", st, msg, started)
	}
	if tk, _ := a.Store.GetTask(ctx, next.ID); tk.Status != "active" {
		t.Fatalf("task paused for a held budget: %s", tk.Status)
	}
	if p := a.Policy.Get(ctx, next.ID); p.ConsecutiveFailures != 0 {
		t.Fatalf("breaker counted the skip: %d", p.ConsecutiveFailures)
	}
	var kind, action string
	if err := a.DB.QueryRowContext(ctx, `SELECT kind, action_taken FROM budget_events ORDER BY at DESC LIMIT 1`).Scan(&kind, &action); err != nil || kind != "held" || action != "skip" {
		t.Fatalf("budget event %q %q %v", kind, action, err)
	}

	// The first run ends: its hold goes, the next run gets the room it left.
	if _, err := a.StopRun(ctx, running); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { _, n := a.Cost.Held("global", "", "", pricing.USD); return n == 0 })
	quick, err := a.startTaskAsync(next.ID, cost.BucketProduction)
	if err != nil || quick == "" {
		t.Fatalf("after: %q %v", quick, err)
	}
	waitFor(t, 10*time.Second, func() bool { st, _, _ := runRow(a, quick); return st == "running" })
	if _, err := a.StopRun(ctx, quick); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for {
		select {
		case e := <-events:
			if e.Kind == EventBudgetBreached || e.Kind == EventRunFailed && e.RunID == id {
				t.Fatalf("unexpected notice: %+v", e)
			}
		default:
			if runs, _ := a.Store.ListRuns(ctx, next.ID, 5); len(runs) != 2 {
				t.Fatalf("runs=%d (a retry started?)", len(runs))
			}
			return
		}
	}
}

// Subscription runs have no money cap: they hold nothing and never block others.
func TestSubscriptionRunHoldsNothing(t *testing.T) {
	a := newTestApp(t)
	o := &runner.Options{Cost: &runner.CostControl{MaxAmount: 0, Currency: pricing.USD}}
	if h, err := a.holdRunBudget(context.Background(), o, cost.BucketProduction, mustProfile(t, a, "deepseek")); h != nil || err != nil {
		t.Fatalf("%v %v", h, err)
	}
	if h, err := a.holdRunBudget(context.Background(), &runner.Options{}, cost.BucketProduction, mustProfile(t, a, "deepseek")); h != nil || err != nil {
		t.Fatalf("no cost control: %v %v", h, err)
	}
}

func mustProfile(t *testing.T, a *App, name string) provider.Profile {
	t.Helper()
	p, err := a.ResolveProfile(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
