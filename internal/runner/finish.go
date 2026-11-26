package runner

import (
	"context"
	"database/sql"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// updateRunFinish persists a terminal run and every related terminal write in
// one transaction. A result, pricing or success-callback failure therefore
// cannot leave a completed run with only part of its outcome saved.
func (r *Runner) updateRunFinish(ctx context.Context, finish db.Run, opts Options, resultText string, cost *pricing.Cost) error {
	tx, err := r.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateRunFinishOn(ctx, tx, finish); err != nil {
		return err
	}
	if resultText != "" {
		if err := db.SetRunResultOn(ctx, tx, finish.ID, resultText); err != nil {
			return err
		}
	}
	if cost != nil {
		if err := db.UpdateRunCostOn(ctx, tx, finish.ID, *cost); err != nil {
			return err
		}
	}
	if finish.Status == "succeeded" && opts.OnSuccess != nil {
		if err := opts.OnSuccess(ctx, tx, finish.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func updateRunFinishOn(ctx context.Context, tx *sql.Tx, r db.Run) error {
	_, err := tx.ExecContext(ctx, `
UPDATE runs SET status=?, session_id=?, exit_code=?, error_message=?, total_cost_usd=?,
  input_tokens=?, output_tokens=?, duration_ms=?, started_at=?, finished_at=?,
  provider_id=?, provider_name=?, model=?
WHERE id=?`,
		r.Status, r.SessionID, r.ExitCode, r.ErrorMessage, r.TotalCostUSD,
		r.InputTokens, r.OutputTokens, r.DurationMS, r.StartedAt, r.FinishedAt,
		r.ProviderID, r.ProviderName, r.Model, r.ID,
	)
	return err
}
