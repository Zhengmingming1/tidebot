package app

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// Unlimited first: a run the owner starts outside a task has no hidden per-run
// cap. Only the optional global daily budget applies, and helper analyses keep
// their own explicit caps.
func TestAdHocRunHasNoHiddenCap(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	clearModeBudgets(t, a)
	runMax, cc, err := a.admitRun(ctx, "", cost.BucketProduction, modeMeteredProfile(), 0)
	if err != nil || runMax != 0 || cc.MaxAmount != 0 {
		t.Fatalf("no budget set: cap %v %+v %v", runMax, cc, err)
	}
	if err = a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "global", Period: "day", Action: "pause_notify", WarnAtPct: 80,
		MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}, Currency: pricing.USD}); err != nil {
		t.Fatal(err)
	}
	if err = a.Cost.RecordEntry(ctx, cost.Entry{RunID: "spent", Bucket: cost.BucketProduction, Amount: 0.25, Currency: pricing.USD, Source: pricing.SourceClaude, Input: 1}); err != nil {
		t.Fatal(err)
	}
	f := a.cachedFX()
	runMax, _, err = a.admitRun(ctx, "", cost.BucketProduction, modeMeteredProfile(), 0)
	if err != nil || math.Abs(f.Convert(runMax, f.Display, pricing.USD)-0.75) > 1e-9 {
		t.Fatalf("global budget left 0.75: %v %v", runMax, err)
	}
	runMax, _, err = a.admitRun(ctx, "", cost.BucketAssist, modeMeteredProfile(), 0.1)
	if err != nil || math.Abs(f.Convert(runMax, f.Display, pricing.USD)-0.1) > 1e-9 {
		t.Fatalf("helper analysis lost its cap: %v %v", runMax, err)
	}
}

func TestAdHocRunArgsCarryNoDefaultCapOrEffort(t *testing.T) {
	a := newTestApp(t)
	clearModeBudgets(t, a)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "hello", TriggerKind: "manual"}, "anthropic", 0)
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("%+v %v", res, err)
	}
	raw, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if contains(args, "--max-budget-usd") || contains(args, "--effort") {
		t.Fatalf("hidden default cap or fixed effort: %s", raw)
	}
	if !contains(args, "--restricted") || argAfter(args, "--permission-mode") != "dontAsk" {
		t.Fatalf("base safety args lost: %s", raw)
	}
}

func TestNewTaskStartsWithoutPerRunCap(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tk, err := a.EnsureTask(ctx, "fresh", "work", "", "deepseek", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// without the global budget a fresh install starts with, nothing caps the run
	if _, err = a.DB.Exec(`DELETE FROM budgets WHERE scope='global'`); err != nil {
		t.Fatal(err)
	}
	runMax, _, err := a.admitRun(ctx, tk.ID, cost.BucketProduction, modeMeteredProfile(), 0)
	if err != nil || runMax != 0 {
		t.Fatalf("new task cap %v %v", runMax, err)
	}
}
