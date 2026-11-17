package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func costSnapshotOpts(id string) *runner.Options {
	return &runner.Options{
		RunID: id,
		Cost: &runner.CostControl{
			Currency:      pricing.USD,
			MaxAmount:     0,
			MaxTokens:     1000,
			MaxRequests:   4,
			BudgetSources: []string{"global:day"},
		},
	}
}

func readCostSnapshot(t *testing.T, a *App, id string) string {
	t.Helper()
	var raw string
	if err := a.DB.QueryRow(`SELECT effective_cost_policy_json FROM runs WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// A canceled run context or a briefly busy database used to fail AtStart at
// once, so a run that could have started was marked skipped. The write is
// retried on its own deadline and must still land.
func TestSaveCostSnapshotRetriesWhenDatabaseBusy(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	insertTestRun(t, a, "cs1")
	blockSQL(t, a, "costsnap", `BEFORE UPDATE ON runs WHEN NEW.effective_cost_policy_json<>''`)

	at := time.Date(2026, 11, 18, 3, 47, 0, 0, time.UTC)
	done := make(chan error, 1)
	go func() {
		done <- a.saveCostSnapshot(costSnapshotOpts("cs1"), "day", at)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return readCostSnapshot(t, a, "cs1") == ""
	})
	time.Sleep(30 * time.Millisecond)
	if raw := readCostSnapshot(t, a, "cs1"); raw != "" {
		t.Fatalf("wrote before unlock: %q", raw)
	}

	unblockSQL(t, a, "costsnap")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("retry failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not finish")
	}
	raw := readCostSnapshot(t, a, "cs1")
	if !strings.Contains(raw, `"mode":"task"`) || !strings.Contains(raw, `"max_tokens":1000`) || !strings.Contains(raw, `"bucket":"day"`) {
		t.Fatalf("snapshot %s", raw)
	}
	if !json.Valid([]byte(raw)) {
		t.Fatalf("invalid json: %s", raw)
	}
	if !strings.Contains(raw, `"accounting_day":"`+at.In(a.Loc()).Format("2006-01-02")+`"`) {
		t.Fatalf("accounting day: %s", raw)
	}
}

func TestSaveCostSnapshotWrites(t *testing.T) {
	a := newTestApp(t)
	insertTestRun(t, a, "cs2")
	if err := a.saveCostSnapshot(costSnapshotOpts("cs2"), "day", time.Now()); err != nil {
		t.Fatal(err)
	}
	if raw := readCostSnapshot(t, a, "cs2"); !strings.Contains(raw, `"mode":"task"`) {
		t.Fatalf("snapshot %s", raw)
	}
}

func TestSaveCostSnapshotGivesUpWhenStillBusy(t *testing.T) {
	wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
	t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
	writeRetryWait, writeRetryPause, writeRetryFor = 20*time.Millisecond, time.Millisecond, 40*time.Millisecond

	a := newTestApp(t)
	insertTestRun(t, a, "cs3")
	blockSQL(t, a, "costsnapfail", `BEFORE UPDATE ON runs WHEN NEW.effective_cost_policy_json<>''`)

	if err := a.saveCostSnapshot(costSnapshotOpts("cs3"), "day", time.Now()); err == nil {
		t.Fatal("expected write to fail")
	}
	if raw := readCostSnapshot(t, a, "cs3"); raw != "" {
		t.Fatalf("saved after giving up: %q", raw)
	}
}
