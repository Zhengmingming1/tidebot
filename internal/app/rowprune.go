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
	// Soft-deleted workflows whose executions are already gone: drop their version
	// definitions (definition_json) after the same week. Capability uninstall scans
	// every definition_json; leaving soft-deleted history forever grows that cost.
	{"workflow_versions_deleted", 7 * 24 * time.Hour,
		`DELETE FROM workflow_versions WHERE workflow_id IN (
			SELECT w.id FROM workflows w
			WHERE w.deleted=1 AND w.updated_at < ?
			AND NOT EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow_id=w.id)
			LIMIT ?)`},
	// Bindings to nodes of those soft-deleted workflows (owner_id is "wf…:node").
	{"capability_bindings_deleted_workflows", 7 * 24 * time.Hour,
		`DELETE FROM capability_bindings WHERE owner_kind='workflow_node' AND owner_id IN (
			SELECT b.owner_id FROM capability_bindings b
			JOIN workflows w ON w.deleted=1 AND w.updated_at < ?
				AND b.owner_kind='workflow_node'
				AND substr(b.owner_id,1,length(w.id)+1)=w.id||':'
			WHERE NOT EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow_id=w.id)
			LIMIT ?)`},
	// Empty soft-deleted workflow stubs (no versions, no runs) after the week.
	{"workflows_deleted", 7 * 24 * time.Hour,
		`DELETE FROM workflows WHERE id IN (
			SELECT w.id FROM workflows w
			WHERE w.deleted=1 AND w.updated_at < ?
			AND NOT EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow_id=w.id)
			AND NOT EXISTS (SELECT 1 FROM workflow_versions v WHERE v.workflow_id=w.id)
			LIMIT ?)`},
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
