package cost

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

func holdService(t *testing.T) *Service {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	s := New(sqlDB, nil)
	if err := s.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func setDayCap(t *testing.T, s *Service, scope, scopeID, bucket string, max float64, cur string) {
	t.Helper()
	if err := s.UpsertBudget(context.Background(), Budget{Scope: scope, ScopeID: scopeID, Period: "day", Bucket: bucket,
		MaxUSD: sql.NullFloat64{Float64: max, Valid: true}, Action: "pause_notify", WarnAtPct: 80, Currency: cur}); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Runs starting together share what is left instead of each taking all of it;
// a run is turned away (Held, not a breach) once less than a cent is left.
func TestReserveRunSharesRemainder(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	setDayCap(t, s, "global", "", "", 1, pricing.USD)
	if err := s.RecordEntry(ctx, Entry{RunID: "run_done", Amount: 0.2, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	h1, c1, chk, err := s.ReserveRun(ctx, "task_a", BucketProduction, 0.5, pricing.USD)
	if err != nil || !chk.OK || !near(c1, 0.5) || h1 == nil {
		t.Fatalf("first: %v %+v %v", c1, chk, err)
	}
	h2, c2, chk, err := s.ReserveRun(ctx, "task_b", BucketProduction, 0.5, pricing.USD)
	if err != nil || !chk.OK || !near(c2, 0.3) {
		t.Fatalf("second should get the 0.30 left: %v %+v %v", c2, chk, err)
	}
	_, _, chk, err = s.ReserveRun(ctx, "task_c", BucketProduction, 0.5, pricing.USD)
	if err != nil || chk.OK || !chk.Held || chk.BreachAction != "skip" || chk.BreachScope != "global" {
		t.Fatalf("third should be held off: %+v %v", chk, err)
	}
	if !strings.Contains(chk.Reason, "daily USD cap held by 2 running (0.2000+0.8000/1.0000)") {
		t.Fatalf("reason %q", chk.Reason)
	}
	// What a run spends moves from its hold to the ledger: counted once.
	h1.SetSpent(0.1, pricing.USD)
	_ = s.RecordEntry(ctx, Entry{RunID: "run_1", Amount: 0.1, Currency: pricing.USD, Source: pricing.SourceClaude})
	if held, n := s.Held("global", "", "", pricing.USD); n != 2 || !near(held, 0.7) {
		t.Fatalf("held %v by %d", held, n)
	}
	sum, _ := s.Summary(ctx, "")
	for _, b := range sum.Budgets {
		if b.Scope == "global" && (!near(b.HeldUSD, 0.7) || b.HeldRuns != 2 || !near(b.UsedUSD, 0.3)) {
			t.Fatalf("summary %+v", b)
		}
	}
	// A run that ends gives back what it didn't spend.
	h1.Release()
	h1.Release() // twice is fine
	_, c3, chk, _ := s.ReserveRun(ctx, "task_c", BucketProduction, 0.5, pricing.USD)
	if !chk.OK || !near(c3, 0.4) {
		t.Fatalf("after release: %v %+v", c3, chk)
	}
	h2.Release()
	var nilHold *Hold
	nilHold.SetSpent(1, pricing.USD)
	nilHold.Release()
}

// Task and bucket budgets only count holds of their own task / bucket; the
// cap is converted between currencies; no money cap means nothing is held.
func TestReserveRunScopesAndCurrency(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	setDayCap(t, s, "task", "task_a", "", 0.3, pricing.USD)
	setDayCap(t, s, "bucket", BucketCanary, BucketCanary, 7.1, pricing.CNY) // = $1
	ha, ca, chk, _ := s.ReserveRun(ctx, "task_a", BucketProduction, 0.25, pricing.USD)
	if !chk.OK || !near(ca, 0.25) {
		t.Fatalf("task a: %v %+v", ca, chk)
	}
	if _, cb, chk, _ := s.ReserveRun(ctx, "task_b", BucketProduction, 0.25, pricing.USD); !chk.OK || !near(cb, 0.25) {
		t.Fatalf("another task is not limited by task a's budget: %v %+v", cb, chk)
	}
	if _, ca2, chk, _ := s.ReserveRun(ctx, "task_a", BucketProduction, 0.25, pricing.USD); !chk.OK || !near(ca2, 0.05) {
		t.Fatalf("task a again: %v %+v", ca2, chk)
	}
	if _, _, chk, _ := s.ReserveRun(ctx, "task_a", BucketProduction, 0.25, pricing.USD); chk.OK || !chk.Held || chk.BreachScope != "task" {
		t.Fatalf("task a third: %+v", chk)
	}
	ha.Release()
	// canary bucket: ¥7.1 cap, run cap given in CNY
	_, cc, chk, _ := s.ReserveRun(ctx, "task_c", BucketCanary, 5, pricing.CNY)
	if !chk.OK || !near(cc, 5) {
		t.Fatalf("canary: %v %+v", cc, chk)
	}
	if held, n := s.Held("bucket", BucketCanary, BucketCanary, pricing.USD); n != 1 || math.Abs(held-5/7.1) > 1e-6 {
		t.Fatalf("canary held in USD %v by %d", held, n)
	}
	if _, cc2, chk, _ := s.ReserveRun(ctx, "task_d", BucketCanary, 5, pricing.CNY); !chk.OK || math.Abs(cc2-2.1) > 1e-6 {
		t.Fatalf("canary second: %v %+v", cc2, chk)
	}
	// subscription runs (no money cap) hold nothing
	if h, c, chk, _ := s.ReserveRun(ctx, "task_e", BucketProduction, 0, pricing.USD); h != nil || c != 0 || !chk.OK {
		t.Fatalf("no cap: %v %v %+v", h, c, chk)
	}
}

// A cap already reached is a real breach (the budget's own action), not Held;
// a hold from before midnight doesn't count against the new day.
func TestReserveRunBreachAndNewDay(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 25, 23, 59, 0, 0, time.Local)
	s.Now = func() time.Time { return now }
	setDayCap(t, s, "global", "", "", 0.5, pricing.USD)
	h, _, chk, _ := s.ReserveRun(ctx, "", BucketProduction, 0.5, pricing.USD)
	if !chk.OK || h == nil {
		t.Fatalf("%+v", chk)
	}
	if _, _, chk, _ := s.ReserveRun(ctx, "", BucketProduction, 0.5, pricing.USD); !chk.Held {
		t.Fatalf("same day: %+v", chk)
	}
	now = now.Add(2 * time.Minute)
	if _, c, chk, _ := s.ReserveRun(ctx, "", BucketProduction, 0.5, pricing.USD); !chk.OK || !near(c, 0.5) {
		t.Fatalf("new day still counts yesterday's hold: %v %+v", c, chk)
	}
	_ = s.RecordEntry(ctx, Entry{RunID: "run_big", Amount: 0.6, Currency: pricing.USD, Source: pricing.SourceClaude})
	_, _, chk, _ = s.ReserveRun(ctx, "", BucketProduction, 0.5, pricing.USD)
	if chk.OK || chk.Held || chk.BreachAction != "pause_notify" || !strings.Contains(chk.Reason, "cap reached") {
		t.Fatalf("reached: %+v", chk)
	}
}

// Many runs reserving at once never hold more than the budget.
func TestReserveRunConcurrent(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	setDayCap(t, s, "global", "", "", 1, pricing.USD)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total, ok := 0.0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, c, chk, err := s.ReserveRun(ctx, "", BucketProduction, 0.3, pricing.USD)
			if err != nil {
				t.Error(err)
				return
			}
			if chk.OK {
				mu.Lock()
				total += c
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if total > 1+1e-9 || ok != 4 {
		t.Fatalf("held %.4f across %d runs", total, ok)
	}
}
