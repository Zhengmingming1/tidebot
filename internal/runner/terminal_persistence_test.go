package runner

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

func TestTerminalPersistenceRollsBackEveryRelatedWrite(t *testing.T) {
	for _, column := range []string{"result_text", "cost_amount"} {
		t.Run(column, func(t *testing.T) {
			r, sqlDB, _ := drainRunner(t, 1)
			r.UsageSaveEvery = -1
			if _, err := sqlDB.Exec(`CREATE TABLE finish_probe(run_id TEXT PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if _, err := sqlDB.Exec(`CREATE TRIGGER reject_terminal_` + column + ` BEFORE UPDATE OF ` + column + ` ON runs WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'forced terminal write failure'); END`); err != nil {
				t.Fatal(err)
			}
			opts := helperOptions(t, "good")
			opts.Cost = &CostControl{Calc: pricing.Calculator{}}
			opts.OnSuccess = func(ctx context.Context, tx *sql.Tx, runID string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO finish_probe(run_id) VALUES(?)`, runID)
				return err
			}
			result, err := r.RunOnce(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), "persist run finish") {
				t.Fatalf("terminal persistence failure was hidden: %+v %v", result, err)
			}
			var status, text string
			var cost sql.NullFloat64
			if err := sqlDB.QueryRow(`SELECT status, result_text, cost_amount FROM runs WHERE id=?`, result.RunID).Scan(&status, &text, &cost); err != nil {
				t.Fatal(err)
			}
			var callbacks int
			if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM finish_probe`).Scan(&callbacks); err != nil {
				t.Fatal(err)
			}
			if status != "running" || text != "" || cost.Valid || callbacks != 0 {
				t.Fatalf("partial terminal commit: status=%s result=%q cost=%+v callbacks=%d", status, text, cost, callbacks)
			}
		})
	}
}

func TestUnstartedPersistenceFailureIsReturned(t *testing.T) {
	r, sqlDB, _ := drainRunner(t, 1)
	if _, err := sqlDB.Exec(`CREATE TRIGGER reject_canceled BEFORE UPDATE ON runs WHEN NEW.status='canceled' BEGIN SELECT RAISE(ABORT,'forced cancel write failure'); END`); err != nil {
		t.Fatal(err)
	}
	opts := helperOptions(t, "good")
	opts.OnStart = func(runID string) { r.Cancel(runID) }
	result, err := r.RunOnce(context.Background(), opts)
	if !errors.Is(err, ErrStoppedByUser) || !strings.Contains(err.Error(), "persist run finish") {
		t.Fatalf("cancel persistence failure was hidden: %+v %v", result, err)
	}
	assertRunStillQueued(t, sqlDB, result.RunID)
}

func TestSkippedPersistenceFailureIsReturned(t *testing.T) {
	r, sqlDB, _ := drainRunner(t, 1)
	if _, err := sqlDB.Exec(`CREATE TRIGGER reject_skipped BEFORE UPDATE ON runs WHEN NEW.status='skipped' BEGIN SELECT RAISE(ABORT,'forced skip write failure'); END`); err != nil {
		t.Fatal(err)
	}
	opts := helperOptions(t, "good")
	opts.StartCheck = func(context.Context) error { return errors.New("forced admission refusal") }
	result, err := r.RunOnce(context.Background(), opts)
	if !errors.Is(err, ErrSkipped) || !strings.Contains(err.Error(), "persist run finish") {
		t.Fatalf("skip persistence failure was hidden: %+v %v", result, err)
	}
	assertRunStillQueued(t, sqlDB, result.RunID)
}

func assertRunStillQueued(t *testing.T, sqlDB *sql.DB, runID string) {
	t.Helper()
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("failed terminal write changed status to %s", status)
	}
}
