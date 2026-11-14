package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// shortLedgerRetry shrinks the pauses between retries of a run's final ledger write.
func shortLedgerRetry(t *testing.T) {
	t.Helper()
	retry := ledgerRetryPause
	ledgerRetryPause = 10 * time.Millisecond
	t.Cleanup(func() { ledgerRetryPause = retry })
}

// blockLedger makes every write of the cost ledger fail ("database is locked")
// while test_block_ledger has a row.
func blockLedger(t *testing.T, a *App) {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE test_block_ledger(x INTEGER)`,
		`INSERT INTO test_block_ledger VALUES(1)`,
		`CREATE TRIGGER test_ledger_busy_ins BEFORE INSERT ON cost_ledger WHEN EXISTS (SELECT 1 FROM test_block_ledger)
BEGIN SELECT RAISE(ABORT, 'database is locked'); END`,
		`CREATE TRIGGER test_ledger_busy_upd BEFORE UPDATE ON cost_ledger WHEN EXISTS (SELECT 1 FROM test_block_ledger)
BEGIN SELECT RAISE(ABORT, 'database is locked'); END`,
	} {
		if _, err := a.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func unblockLedger(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.DB.Exec(`DELETE FROM test_block_ledger`); err != nil {
		t.Fatal(err)
	}
}

// One run's final ledger write failing (tidebot's one database connection busy
// for a few seconds) used to refuse every later run, session turn, task and
// workflow with "accounting unavailable" until tidebot restarted, even with no
// budget set. The write is retried; once it lands, runs start again and the
// ledger has the run.
func TestLedgerWriteFailureRecovers(t *testing.T) {
	shortLedgerRetry(t)
	a := newTestApp(t)
	ctx := context.Background()
	blockLedger(t, a)
	first, err := a.RunPromptEx(ctx, runner.Options{Prompt: "one"}, "", 0)
	if first.RunID == "" {
		t.Fatalf("first run %+v %v", first, err)
	}
	if err == nil || !strings.Contains(err.Error(), "cost ledger persistence failed") {
		t.Fatalf("first run should report the lost write: %v", err)
	}
	// Still busy: nothing new may start without its accounting.
	if _, err := a.RunPromptEx(ctx, runner.Options{Prompt: "blocked"}, "", 0); err == nil || !strings.Contains(err.Error(), "accounting unavailable") {
		t.Fatalf("run while the ledger can't be written: %v", err)
	}
	unblockLedger(t, a)
	waitFor(t, 10*time.Second, func() bool { return a.accountingFailure.Load() == nil })
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM cost_ledger WHERE run_id=?`, first.RunID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ledger row of the first run: %d %v", n, err)
	}
	second, err := a.RunPromptEx(ctx, runner.Options{Prompt: "two"}, "", 0)
	if err != nil || second.Status != "succeeded" {
		t.Fatalf("run after the database recovered: %+v %v", second, err)
	}
}

// With a budget the failed run keeps holding its cap until its cost is in the
// ledger (so the budget can't be overspent meanwhile), then lets it go.
func TestLedgerWriteFailureKeepsHoldUntilWritten(t *testing.T) {
	shortLedgerRetry(t)
	a := newTestApp(t)
	ctx := context.Background()
	if calculator(mustProfile(t, a, "deepseek")).Billing.Mode != pricing.ModeMetered {
		t.Skip("deepseek preset is not metered")
	}
	f := a.cachedFX()
	if err := a.Cost.UpsertBudget(ctx, cost.Budget{Scope: "global", Period: "day", Action: "pause_notify", WarnAtPct: 100,
		MaxUSD: sql.NullFloat64{Float64: f.Convert(5, pricing.USD, f.Display), Valid: true}, Currency: f.Display}); err != nil {
		t.Fatal(err)
	}
	blockLedger(t, a)
	res, _ := a.RunPromptEx(ctx, runner.Options{Prompt: "one"}, "deepseek", 0)
	if res.RunID == "" {
		t.Fatal("no run")
	}
	if _, n := a.Cost.Held("global", "", "", pricing.USD); n != 1 {
		t.Fatalf("hold released before the cost was written (%d holds)", n)
	}
	unblockLedger(t, a)
	waitFor(t, 10*time.Second, func() bool { return a.accountingFailure.Load() == nil })
	if _, n := a.Cost.Held("global", "", "", pricing.USD); n != 0 {
		t.Fatalf("hold kept after the cost was written (%d holds)", n)
	}
	policyMu := a.costPolicyMu()
	policyMu.Lock()
	left := len(a.pendingLedger)
	policyMu.Unlock()
	if left != 0 {
		t.Fatalf("pending entries %d", left)
	}
}

// Quitting before the next retry makes a last try, so the run's cost isn't lost.
func TestLedgerPendingWrittenOnQuit(t *testing.T) {
	retry := ledgerRetryPause
	ledgerRetryPause = time.Hour // the background retry won't come in time
	t.Cleanup(func() { ledgerRetryPause = retry })
	a := newTestApp(t)
	ctx := context.Background()
	blockLedger(t, a)
	res, _ := a.RunPromptEx(ctx, runner.Options{Prompt: "one"}, "", 0)
	if res.RunID == "" || a.accountingFailure.Load() == nil {
		t.Fatal("expected a pending ledger entry")
	}
	unblockLedger(t, a)
	a.flushLedgerOnQuit()
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM cost_ledger WHERE run_id=?`, res.RunID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ledger row: %d %v", n, err)
	}
	if a.accountingFailure.Load() != nil {
		t.Fatal("admission still closed")
	}
}
