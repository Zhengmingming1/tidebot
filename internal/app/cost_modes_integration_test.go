package app

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func costModeTask(t *testing.T, a *App, mode string) string {
	t.Helper()
	tk, err := a.EnsureTask(context.Background(), "cost mode", "test", "", "deepseek", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE tasks SET cost_control_mode=? WHERE id=?`, mode, tk.ID); err != nil {
		t.Fatal(err)
	}
	return tk.ID
}

func clearModeBudgets(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.DB.Exec(`DELETE FROM budgets`); err != nil {
		t.Fatal(err)
	}
}

func modeMeteredProfile() provider.Profile {
	return provider.Profile{Name: "test-metered", Model: "test-model", Billing: &pricing.Billing{Mode: pricing.ModeMetered}}
}

// Every scope and dimension uses the same mode selection. Bypass must not
// accidentally retain the global token check when the money check is skipped.
func TestCostModesAdmissionScopes(t *testing.T) {
	for _, mode := range []string{"task", "global", "bypass"} {
		for _, scope := range []string{"task", "global", "bucket"} {
			for _, dimension := range []string{"money", "tokens"} {
				t.Run(mode+"/"+scope+"/"+dimension, func(t *testing.T) {
					a := newTestApp(t)
					taskID := costModeTask(t, a, mode)
					clearModeBudgets(t, a)
					b := cost.Budget{Scope: scope, Period: "day", Currency: pricing.USD, Action: "skip"}
					if scope == "task" {
						b.ScopeID = taskID
					}
					if scope == "bucket" {
						b.ScopeID = cost.BucketProduction
						b.Bucket = cost.BucketProduction
					}
					if dimension == "money" {
						b.MaxUSD = sql.NullFloat64{Float64: 1, Valid: true}
					} else {
						b.MaxTokens = sql.NullInt64{Int64: 10, Valid: true}
					}
					ctx := context.Background()
					if err := a.Cost.UpsertBudget(ctx, b); err != nil {
						t.Fatal(err)
					}
					if err := a.Cost.RecordEntry(ctx, cost.Entry{RunID: "already-spent", TaskID: taskID, Bucket: cost.BucketProduction, Amount: 2, Currency: pricing.USD, Source: pricing.SourceClaude, Input: 20}); err != nil {
						t.Fatal(err)
					}
					_, cc, err := a.admitRun(ctx, taskID, cost.BucketProduction, modeMeteredProfile(), 0)
					blocked := mode != "bypass" && (mode == "task" || scope != "task")
					if blocked && err == nil {
						t.Fatal("exhausted applicable budget admitted")
					}
					if !blocked && err != nil {
						t.Fatalf("ignored budget blocked: %v", err)
					}
					if !blocked && cc == nil {
						t.Fatal("disabled limits also disabled accounting")
					}
				})
			}
		}
	}
}

func TestCostModesExplicitCapsAndCurrency(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.CNY, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	taskID := costModeTask(t, a, "task")
	clearModeBudgets(t, a)
	if err := a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "task", ScopeID: taskID, Period: "run", MaxUSD: sql.NullFloat64{Float64: 100, Valid: true}, Currency: pricing.CNY}); err != nil {
		t.Fatal(err)
	}
	cap, cc, err := a.admitRun(ctx, taskID, cost.BucketProduction, modeMeteredProfile(), 0)
	if err != nil || math.Abs(cap-100) > 1e-8 || cc.Currency != pricing.CNY {
		t.Fatalf("100 CNY cap: cap=%g cc=%+v err=%v", cap, cc, err)
	}
	for _, mode := range []string{"global", "bypass"} {
		if _, err := a.DB.Exec(`UPDATE tasks SET cost_control_mode=? WHERE id=?`, mode, taskID); err != nil {
			t.Fatal(err)
		}
		cap, cc, err = a.admitRun(ctx, taskID, cost.BucketProduction, modeMeteredProfile(), 0)
		if err != nil || cap != 0 || cc == nil || cc.MaxAmount != 0 {
			t.Fatalf("%s unlimited: cap=%g cc=%+v err=%v", mode, cap, cc, err)
		}
		cap, _, err = a.admitRun(ctx, taskID, cost.BucketProduction, modeMeteredProfile(), 2)
		if err != nil || math.Abs(cap-14) > 1e-8 {
			t.Fatalf("%s caller USD cap converted: %g %v", mode, cap, err)
		}
	}
}

func TestCostModesBypassRetainsProviderQuota(t *testing.T) {
	a := newTestApp(t)
	taskID := costModeTask(t, a, "bypass")
	clearModeBudgets(t, a)
	ctx := context.Background()
	p := provider.Profile{Name: "test-subscription", Billing: &pricing.Billing{Mode: pricing.ModeSubscription, Quota: &pricing.Quota{Window: "day", MaxTokens: 10}}}
	if err := a.Cost.RecordEntry(ctx, cost.Entry{RunID: "provider-spent", Provider: p.Name, Input: 10, Currency: pricing.USD, Source: pricing.SourceSubscription}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.admitRun(ctx, taskID, cost.BucketProduction, p, 0); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("provider quota was bypassed: %v", err)
	}
}

func TestCostModesBudgetArgsNormalize(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetFX(context.Background(), pricing.FX{Display: pricing.CNY, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	p := provider.Profile{Name: "official", Billing: &pricing.Billing{Mode: pricing.ModeMetered}}
	for _, args := range [][]string{{"--effort", "low"}, {"--max-budget-usd", "9", "--max-budget-usd=10", "--effort", "low"}} {
		got := a.budgetArgs(args, p, 14)
		n := 0
		for _, v := range got {
			if v == "--max-budget-usd" || strings.HasPrefix(v, "--max-budget-usd=") {
				n++
			}
		}
		if n != 1 || math.Abs(callerCapUSD(got)-2) > 1e-8 {
			t.Fatalf("want one actual USD cap, got %v", got)
		}
		got = a.budgetArgs(args, p, 0)
		for _, v := range got {
			if strings.HasPrefix(v, "--max-budget-usd") {
				t.Fatalf("unlimited keeps flag: %v", got)
			}
		}
	}
}

// OnStart happens after initial admission and before the actual start. A change
// here must be picked up even if this run obtains a slot without waiting.
func TestCostModesFreezeAtActualStart(t *testing.T) {
	a := newTestApp(t)
	taskID := costModeTask(t, a, "bypass")
	clearModeBudgets(t, a)
	ctx := context.Background()
	if err := a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "global", Period: "day", Action: "skip", Currency: pricing.USD, MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Cost.RecordEntry(ctx, cost.Entry{RunID: "spent-before-launch", Amount: 2, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	res, err := a.RunPromptEx(ctx, runner.Options{TaskID: taskID, Prompt: "must not start", WorkspaceDir: t.TempDir(), OnStart: func(string) {
		if _, e := a.DB.Exec(`UPDATE tasks SET cost_control_mode='global' WHERE id=?`, taskID); e != nil {
			t.Error(e)
		}
	}}, "deepseek", time.Minute)
	if err == nil || res.Status != "skipped" {
		t.Fatalf("new shared mode not checked at actual start: %+v %v", res, err)
	}
}

func TestCostModesFXConversionIsAtomic(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.USD, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	taskID := costModeTask(t, a, "bypass")
	clearModeBudgets(t, a)
	if err := a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "task", ScopeID: taskID, Period: "run", MaxUSD: sql.NullFloat64{Float64: 2, Valid: true}, Currency: pricing.USD}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_budget_conversion BEFORE UPDATE ON budgets BEGIN SELECT RAISE(ABORT, 'conversion blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.CNY, USDCNY: 7}); err == nil {
		t.Fatal("conversion error ignored")
	}
	if f := a.FX(ctx); f.Display != pricing.USD {
		t.Fatalf("persisted FX partially changed: %+v", f)
	}
	if f := a.cachedFX(); f.Display != pricing.USD {
		t.Fatalf("cached FX partially changed: %+v", f)
	}
	var amount float64
	var currency string
	if err := a.DB.QueryRow(`SELECT max_usd,currency FROM budgets WHERE scope='task' AND scope_id=? AND period='run'`, taskID).Scan(&amount, &currency); err != nil {
		t.Fatal(err)
	}
	if amount != 2 || currency != pricing.USD {
		t.Fatalf("failed conversion changed budget: %g %s", amount, currency)
	}
	if _, err := a.DB.Exec(`DROP TRIGGER fail_budget_conversion`); err != nil {
		t.Fatal(err)
	}
	if err := a.SetFX(ctx, pricing.FX{Display: pricing.CNY, USDCNY: 7}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(`SELECT max_usd,currency FROM budgets WHERE scope='task' AND scope_id=? AND period='run'`, taskID).Scan(&amount, &currency); err != nil {
		t.Fatal(err)
	}
	if amount != 14 || currency != pricing.CNY {
		t.Fatalf("disabled budget not converted: %g %s", amount, currency)
	}
	var mode string
	if err := a.DB.QueryRow(`SELECT cost_control_mode FROM tasks WHERE id=?`, taskID).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "bypass" {
		t.Fatalf("FX conversion changed mode: %s", mode)
	}
}

func TestCostModesBypassRunStillRecordsUsage(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	taskID := costModeTask(t, a, "bypass")
	clearModeBudgets(t, a)
	if err := a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "global", Period: "day", Action: "skip", Currency: pricing.USD, MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Cost.RecordEntry(ctx, cost.Entry{RunID: "spent-global", Amount: 2, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	res, err := a.RunPromptEx(ctx, runner.Options{TaskID: taskID, Prompt: "record usage", WorkspaceDir: t.TempDir()}, "deepseek", time.Minute)
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("bypass failed: %+v %v", res, err)
	}
	var tokens int64
	var source, snapshot string
	if err := a.DB.QueryRow(`SELECT tokens,source FROM cost_ledger WHERE run_id=?`, res.RunID).Scan(&tokens, &source); err != nil {
		t.Fatal(err)
	}
	if tokens <= 0 || source == "" {
		t.Fatalf("bypass lost usage accounting: tokens=%d source=%q", tokens, source)
	}
	if err := a.DB.QueryRow(`SELECT effective_cost_policy_json FROM runs WHERE id=?`, res.RunID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `"mode":"bypass"`) {
		t.Fatalf("missing launch policy: %s", snapshot)
	}
	if money, n := a.Cost.Held("global", "", "", pricing.USD); money != 0 || n != 0 {
		t.Fatalf("bypass leaked global hold: %g (%d)", money, n)
	}
}

func TestCostModesProviderReservationRemainsIndependent(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	p := provider.Profile{Name: "subscription-reservation", Billing: &pricing.Billing{Mode: pricing.ModeSubscription, Quota: &pricing.Quota{Window: "day", MaxTokens: 100, MaxRequests: 10}}}
	first := &runner.Options{RunID: "quota-first", Cost: &runner.CostControl{MaxTokens: 100, MaxRequests: 10}}
	if err := a.reserveProviderQuota(ctx, first, p); err != nil {
		t.Fatal(err)
	}
	second := &runner.Options{RunID: "quota-second", Cost: &runner.CostControl{MaxTokens: 100, MaxRequests: 10}}
	if err := a.reserveProviderQuota(ctx, second, p); err == nil {
		t.Fatal("provider quota was allocated twice")
	}
	delete(a.quotaHolds, first.RunID)
	if err := a.reserveProviderQuota(ctx, second, p); err != nil {
		t.Fatalf("released provider quota unavailable: %v", err)
	}
}
