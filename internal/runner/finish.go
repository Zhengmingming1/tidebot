package runner

import (
	"context"
	"database/sql"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// updateRunFinish persists a terminal run. Successful runs may have an
// OnSuccess callback; it runs in the same transaction so a run cannot be
// recorded as complete while its related success updates are only half done.
func (r *Runner) updateRunFinish(ctx context.Context, finish db.Run, opts Options) error {
	if finish.Status != "succeeded" || opts.OnSuccess == nil {
		return r.store.UpdateRunFinish(ctx, finish)
	}
	tx, err := r.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateRunFinishOn(ctx, tx, finish); err != nil {
		return err
	}
	if err := opts.OnSuccess(ctx, tx, finish.ID); err != nil {
		return err
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
