package app

import (
	"context"
	"testing"
	"time"
)

// A long run's tokens reach the spend ledger while it is still going, so budgets
// and the spend page count them and a crash or power loss doesn't reset them to 0.
func TestLedgerUpdatedMidRun(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "60")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.1")
	t.Setenv("TIDEBOT_MOCK_STEP_USAGE", "1")
	a := newTestApp(t)
	a.Runner.UsageSaveEvery = 50 * time.Millisecond
	a.Cfg.ShutdownGrace = 50 * time.Millisecond
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		_, _ = a.RunPrompt(ctx, "long one", t.TempDir(), "", "", "manual", 0)
		close(done)
	}()
	var runID, status string
	var reqs, tokens int64
	waitFor(t, 10*time.Second, func() bool {
		err := a.DB.QueryRowContext(ctx, `SELECT l.run_id, r.status, l.requests, l.tokens FROM cost_ledger l JOIN runs r ON r.id=l.run_id`).
			Scan(&runID, &status, &reqs, &tokens)
		return err == nil && reqs >= 2
	})
	if status != "running" {
		t.Fatalf("ledger row only after the run ended (status %s)", status)
	}
	if tokens != reqs*1500 {
		t.Fatalf("ledger tokens %d for %d requests", tokens, reqs)
	}
	a.drainRuns()
	<-done
	var n int
	var final int64
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*), MAX(requests) FROM cost_ledger WHERE run_id=?`, runID).Scan(&n, &final); err != nil {
		t.Fatal(err)
	}
	if n != 1 || final < reqs {
		t.Fatalf("ledger rows %d, final requests %d (mid-run %d)", n, final, reqs)
	}
}
