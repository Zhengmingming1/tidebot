package cost

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestBudgetEnforcementAndLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB, nil)
	ctx := context.Background()
	if err := s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	// tiny global daily cap
	if err := s.UpsertBudget(ctx, Budget{
		Scope: "global", Period: "day",
		MaxUSD: sql.NullFloat64{Float64: 0.01, Valid: true},
		Action: "pause_notify", WarnAtPct: 50,
	}); err != nil {
		t.Fatal(err)
	}
	ok, err := s.CheckBeforeRun(ctx, "task_x", BucketProduction, 0.05)
	if err != nil || !ok.OK {
		t.Fatalf("%+v %v", ok, err)
	}
	if ok.RunMaxUSD > 0.01 {
		t.Fatalf("run max should be tightened: %v", ok.RunMaxUSD)
	}
	// seed a fake run id for FK-less ledger (no FK to runs in our migration)
	if err := s.Record(ctx, "run_a", "task_x", BucketProduction, 0.01, 100, 50); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.CheckBeforeRun(ctx, "task_x", BucketProduction, 0.05)
	if err != nil || blocked.OK {
		t.Fatalf("expected block %+v", blocked)
	}
	if err := s.LogBreach(ctx, "global", "", BucketProduction, "cap", blocked.Reason, "pause_notify"); err != nil {
		t.Fatal(err)
	}
	sum, err := s.Summary(ctx, "")
	if err != nil || sum.TotalUSD < 0.01 {
		t.Fatalf("%+v %v", sum, err)
	}
	// assist still blocked by global; raise global then assist-only cap applies
	if err := s.UpsertBudget(ctx, Budget{
		Scope: "global", Period: "day",
		MaxUSD: sql.NullFloat64{Float64: 10, Valid: true},
		Action: "pause_notify", WarnAtPct: 80,
	}); err != nil {
		t.Fatal(err)
	}
	asst, err := s.CheckBeforeRun(ctx, "", BucketAssist, 0.5)
	if err != nil || !asst.OK {
		t.Fatalf("assist should be ok under raised global %+v", asst)
	}
	// Legacy category limits must not be recreated by callers.
	if err := s.UpsertBudget(ctx, Budget{Scope: "bucket", ScopeID: BucketAssist, Period: "day", Bucket: BucketAssist}); err == nil {
		t.Fatal("accepted removed category budget")
	}
	_ = s.Record(ctx, "run_assist", "", BucketAssist, 0.001, 1, 1)
	allowed, err := s.CheckBeforeRun(ctx, "", BucketAssist, 0.5)
	if err != nil || !allowed.OK {
		t.Fatalf("assist uses only shared global cap: %+v %v", allowed, err)
	}
}

// 0 is no per-run cap (no hidden $0.05): only budgets the owner set apply.
func TestCheckBeforeRunZeroIsUnlimited(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB, nil)
	ctx := context.Background()
	chk, err := s.CheckBeforeRun(ctx, "", BucketProduction, 0)
	if err != nil || !chk.OK || chk.RunMaxUSD != 0 {
		t.Fatalf("no budget: %+v %v", chk, err)
	}
	if err = s.UpsertBudget(ctx, Budget{Scope: "global", Period: "day", Action: "pause_notify", MaxUSD: sql.NullFloat64{Float64: 2, Valid: true}, Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	chk, err = s.CheckBeforeRun(ctx, "", BucketProduction, 0)
	if err != nil || !chk.OK || chk.RunMaxUSD != 2 {
		t.Fatalf("global budget: %+v %v", chk, err)
	}
	chk, _ = s.CheckBeforeRun(ctx, "", BucketProduction, 0.5)
	if chk.RunMaxUSD != 0.5 {
		t.Fatalf("explicit cap: %+v", chk)
	}
}
