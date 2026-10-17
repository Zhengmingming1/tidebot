package db

import (
	"context"
	"encoding/json"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// UpdateRunCost stores tidebot's pricing of a run: amount, currency, source,
// token breakdown, the price snapshot used (so later price edits don't change
// history) and the per-model calculation lines.
func (s *Store) UpdateRunCost(ctx context.Context, runID string, c pricing.Cost) error {
	snap, _ := json.Marshal(c.Snapshot)
	detail, _ := json.Marshal(c)
	_, err := s.DB.ExecContext(ctx, `
UPDATE runs SET cost_amount=?, cost_currency=?, cost_source=?, cache_read_tokens=?, cache_write_tokens=?,
  requests=?, price_snapshot=?, cost_detail=?, input_tokens=?, output_tokens=?
WHERE id=?`, c.Amount, c.Currency, c.Source, c.Totals.CacheRead, c.Totals.CacheWrite,
		c.Totals.Requests, string(snap), string(detail), c.Totals.Input, c.Totals.Output, runID)
	return err
}
