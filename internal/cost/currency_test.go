package cost

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

func TestCurrencyBudgetsAndQuota(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	fx := pricing.FX{Display: pricing.USD, USDCNY: 7}
	s := New(sqlDB, nil)
	s.FX = func() pricing.FX { return fx }
	if err := s.UpsertBudget(ctx, Budget{Scope: "global", Period: "day", MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	// 3.5 CNY + 0.25 USD = 0.75 USD
	_ = s.RecordEntry(ctx, Entry{RunID: "r1", Provider: "kimi-cn", Amount: 3.5, Currency: pricing.CNY, Source: pricing.SourcePriced, Input: 100, Requests: 2})
	_ = s.RecordEntry(ctx, Entry{RunID: "r2", Provider: "zai", Amount: 0.25, Currency: pricing.USD, Source: pricing.SourcePriced, Input: 50, Requests: 1})
	chk, err := s.CheckBeforeRun(ctx, "", "", 10)
	if err != nil || !chk.OK || math.Abs(chk.RunMaxUSD-0.25) > 1e-9 || chk.Currency != "USD" {
		t.Fatalf("%+v %v", chk, err)
	}
	// switch display to CNY: budget becomes 7 CNY, remaining 1.75 CNY
	fx.Display = pricing.CNY
	if err := s.ConvertBudgets(ctx, fx, pricing.CNY); err != nil {
		t.Fatal(err)
	}
	chk, _ = s.CheckBeforeRun(ctx, "", "", 10)
	if math.Abs(chk.RunMaxUSD-1.75) > 1e-9 || chk.Currency != "CNY" {
		t.Fatalf("%+v", chk)
	}
	sum, _ := s.Summary(ctx, "")
	if sum.Currency != "CNY" || math.Abs(sum.TotalUSD-5.25) > 1e-9 {
		t.Fatalf("%+v", sum)
	}
	tok, req, _, err := s.QuotaUsage(ctx, "kimi-cn", "5h")
	if err != nil || tok != 100 || req != 2 {
		t.Fatalf("quota %d %d %v", tok, req, err)
	}
}

func TestLegacyMigration(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	// rows written the old way get the legacy marking from Record's callers;
	// the migration itself marked pre-existing rows — check the column defaults exist.
	if _, err := sqlDB.Exec(`SELECT cost_amount, cost_currency, cost_source, price_snapshot, cost_detail FROM runs LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`SELECT amount, currency, source, provider, model, requests FROM cost_ledger LIMIT 1`); err != nil {
		t.Fatal(err)
	}
}
