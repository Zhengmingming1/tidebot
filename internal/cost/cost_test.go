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
	// hit assist daily cap
	if err := s.UpsertBudget(ctx, Budget{
		Scope: "bucket", ScopeID: BucketAssist, Period: "day", Bucket: BucketAssist,
		MaxUSD: sql.NullFloat64{Float64: 0.001, Valid: true},
		Action: "pause_notify",
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Record(ctx, "run_assist", "", BucketAssist, 0.001, 1, 1)
	blockedAsst, err := s.CheckBeforeRun(ctx, "", BucketAssist, 0.5)
	if err != nil || blockedAsst.OK {
		t.Fatalf("assist bucket cap should block %+v", blockedAsst)
	}
}
