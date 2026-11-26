package runner

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSuccessfulFinishCallbackCommitsWithRun(t *testing.T) {
	r, sqlDB, _ := drainRunner(t, 1)
	if _, err := sqlDB.Exec(`CREATE TABLE finish_probe(run_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	opts := helperOptions(t, "good")
	opts.OnSuccess = func(ctx context.Context, tx *sql.Tx, runID string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO finish_probe(run_id) VALUES(?)`, runID)
		return err
	}
	result, err := r.RunOnce(context.Background(), opts)
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var status, probe string
	if err := sqlDB.QueryRow(`SELECT status FROM runs WHERE id=?`, result.RunID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT run_id FROM finish_probe WHERE run_id=?`, result.RunID).Scan(&probe); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || probe != result.RunID {
		t.Fatalf("run status=%s probe=%s", status, probe)
	}
}

func TestSuccessfulFinishCallbackFailureRollsBackRunFinish(t *testing.T) {
	r, sqlDB, _ := drainRunner(t, 1)
	opts := helperOptions(t, "good")
	opts.OnSuccess = func(context.Context, *sql.Tx, string) error {
		return errors.New("forced success callback failure")
	}
	result, err := r.RunOnce(context.Background(), opts)
	if err == nil || result.Status != "succeeded" {
		t.Fatalf("callback failure was hidden: result=%+v err=%v", result, err)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM runs WHERE id=?`, result.RunID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("failed success transaction changed terminal state to %s", status)
	}
}
