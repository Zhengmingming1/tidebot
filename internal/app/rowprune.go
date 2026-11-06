package app

import (
	"context"
	"time"
)

// Database rows that only ever grow are trimmed with the file cleanup (every 6 hours and
// from Settings › Data). Uptime Kuma does the same for its heartbeat table
// (server/jobs/clear-old-data.js: delete rows older than the keep period, then
// PRAGMA optimize). tidebot's keep periods are fixed: these rows are logs nobody browses
// past the first screen. Task run history and spend stay; workflow execution rows (with
// their large node outputs) are trimmed after the keep period below.
type rowRule struct {
	name  string
	keep  time.Duration
	query string // deletes one batch: args are the cutoff (UTC RFC3339) and the batch size
}

const (
	pruneBatch      = 2000
	pruneMaxBatches = 50 // per table per pass: a huge backlog is worked off over a few passes
)

var rowRules = []rowRule{
	// Sign-in attempts: the rate limit looks back 15 minutes.
	{"login_attempts", 30 * 24 * time.Hour,
		`DELETE FROM login_attempts WHERE id IN (SELECT id FROM login_attempts WHERE at < ? LIMIT ?)`},
	// Notifications cleared from the bell: hidden already, kept a month for reference.
	{"notifications_cleared", 30 * 24 * time.Hour,
		`DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE cleared_at <> '' AND status <> 'queued' AND created_at < ? LIMIT ?)`},
	// Any other notification: the bell lists the newest 50. Queued ones still wait for the digest.
	{"notifications", 180 * 24 * time.Hour,
		`DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE status <> 'queued' AND created_at < ? LIMIT ?)`},
	// Budget breaches: the spend page shows the newest 10.
	{"budget_events", 365 * 24 * time.Hour,
		`DELETE FROM budget_events WHERE id IN (SELECT id FROM budget_events WHERE at < ? LIMIT ?)`},
	// Workflow executions: each finished run can keep up to 32×128 KB of node output in
	// the DB. Soft-deleted workflows still leave that history. Drop finished rows past
	// 90 days (queued/running stay). Nodes go with them via ON DELETE CASCADE.
	{"workflow_runs", 90 * 24 * time.Hour,
		`DELETE FROM workflow_runs WHERE id IN (SELECT id FROM workflow_runs WHERE created_at < ? AND status NOT IN ('queued','running') LIMIT ?)`},
	// Soft-deleted workflows: their finished executions are unreachable from the list,
	// so reclaim after a week instead of waiting the full 90 days.
	{"workflow_runs_deleted", 7 * 24 * time.Hour,
		`DELETE FROM workflow_runs WHERE id IN (SELECT r.id FROM workflow_runs r JOIN workflows w ON w.id=r.workflow_id WHERE w.deleted=1 AND r.created_at < ? AND r.status NOT IN ('queued','running') LIMIT ?)`},
}

// pruneRows deletes rows past their keep period in batches, so a long backlog never holds
// the write lock for long. Returns how many rows went; stops early when ctx ends.
func (a *App) pruneRows(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for _, r := range rowRules {
		cut := now.Add(-r.keep).UTC().Format(time.RFC3339)
		for i := 0; i < pruneMaxBatches; i++ {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			res, err := a.DB.ExecContext(ctx, r.query, cut, pruneBatch)
			if err != nil {
				return total, err
			}
			n, _ := res.RowsAffected()
			total += n
			if n < pruneBatch {
				break
			}
		}
	}
	return total, nil
}
