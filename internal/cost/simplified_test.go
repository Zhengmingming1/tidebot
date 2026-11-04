package cost

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// Old databases (and restored backups) cannot silently reinstate removed caps.
func TestRemovedBudgetCapsIgnoredAndMigrated(t *testing.T) {
	s := holdService(t)
	ctx := context.Background()
	_, err := s.DB.Exec(`UPDATE budgets SET max_tokens=1, max_usd=7, action='skip', warn_at_pct=60 WHERE scope='global';
 INSERT INTO budgets(id,scope,scope_id,period,max_usd,max_tokens,action,warn_at_pct,bucket,created_at,updated_at,currency)
 VALUES('old-assist','bucket','assist','day',0.001,1,'skip',80,'assist','','','USD')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, "legacy-spend", "t", BucketAssist, .01, 100, 100); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{BucketAssist, BucketOptimization, BucketCanary, BucketProduction} {
		chk, err := s.CheckBeforeRun(ctx, "t", bucket, .5)
		if err != nil || !chk.OK {
			t.Fatalf("legacy %s: %+v %v", bucket, chk, err)
		}
		h, _, chk, err := s.ReserveRunPolicy(ctx, "t", bucket, .5, pricing.USD, ModeGlobal)
		if err != nil || !chk.OK || chk.RunMaxTokens != 0 {
			t.Fatalf("policy %s: %+v %v", bucket, chk, err)
		}
		h.Release()
	}
	sum, err := s.Summary(ctx, "")
	if err != nil || len(sum.Budgets) != 1 || sum.Budgets[0].MaxTokens != 0 {
		t.Fatalf("summary %+v %v", sum, err)
	}
	// Reapply only migration 42, simulating an upgrade from its predecessor.
	if _, err := s.DB.Exec(`DELETE FROM schema_migrations WHERE version=42`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(s.DB); err != nil {
		t.Fatal(err)
	}
	b, err := s.FindBudget(ctx, "global", "", "day", "")
	if err != nil || b.MaxTokens.Valid || b.MaxUSD.Float64 != 7 || b.Action != "skip" || b.WarnAtPct != 60 {
		t.Fatalf("global settings lost: %+v %v", b, err)
	}
	if _, err := s.FindBudget(ctx, "bucket", BucketAssist, "day", BucketAssist); err != sql.ErrNoRows {
		t.Fatalf("category remains: %v", err)
	}
	if err := s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	sum, err = s.Summary(ctx, "")
	if err != nil || len(sum.Budgets) != 1 || sum.TotalTokens != 200 || sum.TotalUSD != .01 {
		t.Fatalf("ledger/defaults: %+v %v", sum, err)
	}
}

func TestRemovedBudgetWritesRejected(t *testing.T) {
	s := holdService(t)
	for _, b := range []Budget{
		{Scope: "bucket", ScopeID: BucketAssist, Period: "day", Bucket: BucketAssist},
		{Scope: "global", Period: "day", MaxTokens: sql.NullInt64{Int64: 1, Valid: true}},
		{Scope: "global", Period: "run"},
	} {
		if err := s.UpsertBudget(context.Background(), b); err == nil {
			t.Fatalf("accepted %+v", b)
		}
	}
}
