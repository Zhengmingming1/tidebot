package app

import (
	"context"
	"strings"
	"time"
)

// Database rows that only ever grow are trimmed with the file cleanup (every 6 hours and
// from Settings › Data). Uptime Kuma does the same for its heartbeat table
// (server/jobs/clear-old-data.js: delete rows older than the keep period, then
// PRAGMA optimize). Most of tidebot's keep periods are fixed: these rows are logs
// nobody browses past the first screen. Task run history and spend stay.
// Finished workflow execution rows (with their large node outputs) follow
// RetentionPolicy.WorkflowRunDays (default 90; 0 = keep forever).
type rowRule struct {
	name  string
	table string
	keep  time.Duration
	query string // deletes one batch: args are the cutoff (UTC RFC3339) and the batch size
	match string // SELECT a unique key for rows this rule would delete; arg is the cutoff only
}

const (
	pruneBatch              = 2000
	pruneMaxBatches         = 50 // per table per pass: a huge backlog is worked off over a few passes
	softDeletedWorkflowKeep = 7 * 24 * time.Hour
)

var rowRules = []rowRule{
	// Sign-in attempts: the rate limit looks back 15 minutes.
	{name: "login_attempts", table: "login_attempts", keep: 30 * 24 * time.Hour,
		query: `DELETE FROM login_attempts WHERE id IN (SELECT id FROM login_attempts WHERE at < ? LIMIT ?)`,
		match: `SELECT id FROM login_attempts WHERE at < ?`},
	// Notifications cleared from the bell: hidden already, kept a month for reference.
	{name: "notifications_cleared", table: "notifications", keep: 30 * 24 * time.Hour,
		query: `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE cleared_at <> '' AND status <> 'queued' AND created_at < ? LIMIT ?)`,
		match: `SELECT id FROM notifications WHERE cleared_at <> '' AND status <> 'queued' AND created_at < ?`},
	// Any other notification: the bell lists the newest 50. Queued ones still wait for the digest.
	{name: "notifications", table: "notifications", keep: 180 * 24 * time.Hour,
		query: `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE status <> 'queued' AND created_at < ? LIMIT ?)`,
		match: `SELECT id FROM notifications WHERE status <> 'queued' AND created_at < ?`},
	// Budget breaches: the spend page shows the newest 10.
	{name: "budget_events", table: "budget_events", keep: 365 * 24 * time.Hour,
		query: `DELETE FROM budget_events WHERE id IN (SELECT id FROM budget_events WHERE at < ? LIMIT ?)`,
		match: `SELECT id FROM budget_events WHERE at < ?`},
	// Capability proposals (the install / update / uninstall cards in a session):
	// payload_json holds the whole lock, diff and scan (up to 1 MB). A pending one
	// expires after 24 h but its row keeps status 'pending'; nothing ever removed
	// them. Proposals of a deleted session can no longer be confirmed: drop them
	// after a day. Every other settled or expired one goes after 30 days (the
	// capability's own event log keeps the audit trail). 'applying' stays: startup
	// recovery turns an interrupted one into 'failed'.
	{name: "capability_proposals_orphan", table: "capability_proposals", keep: 24 * time.Hour,
		query: `DELETE FROM capability_proposals WHERE id IN (
			SELECT p.id FROM capability_proposals p
			WHERE p.status<>'applying' AND p.created_at < ?
			AND NOT EXISTS (SELECT 1 FROM chat_sessions s WHERE s.id=p.session_id)
			LIMIT ?)`,
		match: `SELECT p.id FROM capability_proposals p
			WHERE p.status<>'applying' AND p.created_at < ?
			AND NOT EXISTS (SELECT 1 FROM chat_sessions s WHERE s.id=p.session_id)`},
	{name: "capability_proposals", table: "capability_proposals", keep: 30 * 24 * time.Hour,
		query: `DELETE FROM capability_proposals WHERE id IN (SELECT id FROM capability_proposals WHERE status<>'applying' AND created_at < ? LIMIT ?)`,
		match: `SELECT id FROM capability_proposals WHERE status<>'applying' AND created_at < ?`},
}

// workflowRowRules is the keep period for finished workflow execution history
// and the shorter reclaim of soft-deleted workflows. WorkflowRunDays 0 means
// keep forever (no workflow-related rule runs). Soft-deleted history uses
// min(7 days, WorkflowRunDays) so it is never kept longer than live history,
// and stays a one-week reclaim when the user leaves the 90-day default.
// queued/running executions are never deleted.
func workflowRowRules(days int) []rowRule {
	if days <= 0 {
		return nil
	}
	keep := time.Duration(days) * 24 * time.Hour
	soft := softDeletedWorkflowKeep
	if keep < soft {
		soft = keep
	}
	return []rowRule{
		// Workflow executions: each finished run can keep up to 32×128 KB of node
		// output in the DB. Soft-deleted workflows still leave that history.
		// Drop finished rows past the keep period (queued/running stay). Nodes
		// go with them via ON DELETE CASCADE.
		{name: "workflow_runs", table: "workflow_runs", keep: keep,
			query: `DELETE FROM workflow_runs WHERE id IN (SELECT id FROM workflow_runs WHERE created_at < ? AND status NOT IN ('queued','running') LIMIT ?)`,
			match: `SELECT id FROM workflow_runs WHERE created_at < ? AND status NOT IN ('queued','running')`},
		// Soft-deleted workflows: their finished executions are unreachable from
		// the list, so reclaim after a week (or sooner if the keep period is shorter).
		{name: "workflow_runs_deleted", table: "workflow_runs", keep: soft,
			query: `DELETE FROM workflow_runs WHERE id IN (SELECT r.id FROM workflow_runs r JOIN workflows w ON w.id=r.workflow_id WHERE w.deleted=1 AND r.created_at < ? AND r.status NOT IN ('queued','running') LIMIT ?)`,
			match: `SELECT r.id FROM workflow_runs r JOIN workflows w ON w.id=r.workflow_id WHERE w.deleted=1 AND r.created_at < ? AND r.status NOT IN ('queued','running')`},
		// Soft-deleted workflows whose executions are already gone: drop their version
		// definitions (definition_json) after the same period. Capability uninstall scans
		// every definition_json; leaving soft-deleted history forever grows that cost.
		{name: "workflow_versions_deleted", table: "workflow_versions", keep: soft,
			query: `DELETE FROM workflow_versions WHERE workflow_id IN (
			SELECT w.id FROM workflows w
			WHERE w.deleted=1 AND w.updated_at < ?
			AND NOT EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow_id=w.id)
			LIMIT ?)`,
			// Preview treats finished runs this pass would already drop as gone,
			// so versions that become eligible after those deletes are counted.
			// Both ? placeholders are the soft cutoff.
			match: `SELECT v.workflow_id || ':' || v.version FROM workflow_versions v
			WHERE v.workflow_id IN (
			SELECT w.id FROM workflows w
			WHERE w.deleted=1 AND w.updated_at < ?
			AND NOT EXISTS (
				SELECT 1 FROM workflow_runs r
				WHERE r.workflow_id=w.id
				AND (r.status IN ('queued','running') OR r.created_at >= ?)))`},
		// Bindings to nodes of those soft-deleted workflows (owner_id is "wf…:node").
		{name: "capability_bindings_deleted_workflows", table: "capability_bindings", keep: soft,
			query: `DELETE FROM capability_bindings WHERE owner_kind='workflow_node' AND owner_id IN (
			SELECT b.owner_id FROM capability_bindings b
			JOIN workflows w ON w.deleted=1 AND w.updated_at < ?
				AND b.owner_kind='workflow_node'
				AND substr(b.owner_id,1,length(w.id)+1)=w.id||':'
			WHERE NOT EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow_id=w.id)
			LIMIT ?)`,
			match: `SELECT rowid FROM capability_bindings WHERE owner_kind='workflow_node' AND owner_id IN (
			SELECT b.owner_id FROM capability_bindings b
			JOIN workflows w ON w.deleted=1 AND w.updated_at < ?
				AND b.owner_kind='workflow_node'
				AND substr(b.owner_id,1,length(w.id)+1)=w.id||':'
			WHERE NOT EXISTS (
				SELECT 1 FROM workflow_runs r
				WHERE r.workflow_id=w.id
				AND (r.status IN ('queued','running') OR r.created_at >= ?)))`},
		// Empty soft-deleted workflow stubs (no versions, no runs) after the same period.
		{name: "workflows_deleted", table: "workflows", keep: soft,
			query: `DELETE FROM workflows WHERE id IN (
			SELECT w.id FROM workflows w
			WHERE w.deleted=1 AND w.updated_at < ?
			AND NOT EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow_id=w.id)
			AND NOT EXISTS (SELECT 1 FROM workflow_versions v WHERE v.workflow_id=w.id)
			LIMIT ?)`,
			// Same-pass: if no queued/running or still-in-period runs remain,
			// versions of this stub would already have been dropped above.
			// Both ? placeholders are the soft cutoff.
			match: `SELECT w.id FROM workflows w
			WHERE w.deleted=1 AND w.updated_at < ?
			AND NOT EXISTS (
				SELECT 1 FROM workflow_runs r
				WHERE r.workflow_id=w.id
				AND (r.status IN ('queued','running') OR r.created_at >= ?))`},
	}
}

func pruneRuleSet(p RetentionPolicy) []rowRule {
	return append(append([]rowRule{}, rowRules...), workflowRowRules(p.WorkflowRunDays)...)
}

// pruneRows deletes rows past their keep period in batches, so a long backlog never holds
// the write lock for long. Workflow execution history follows p.WorkflowRunDays; the
// other tables use fixed periods. Returns how many rows went; stops early when ctx ends.
func (a *App) pruneRows(ctx context.Context, p RetentionPolicy, now time.Time) (int64, error) {
	var total int64
	for _, r := range pruneRuleSet(p) {
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

// previewPruneRows counts the rows pruneRows would delete under p, without
// deleting anything. Counts are all matching rows (not capped by
// pruneBatch*pruneMaxBatches): a huge backlog may need several passes, and
// the confirm shows the full remaining count so it stays honest. Overlapping
// rules on the same table are UNION'd so a row is counted once, matching
// pruneRows. SELECT only; no write lock.
func (a *App) previewPruneRows(ctx context.Context, p RetentionPolicy, now time.Time) (int64, error) {
	type clause struct {
		match string
		cut   string
	}
	var order []string
	grouped := map[string][]clause{}
	for _, r := range pruneRuleSet(p) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		cut := now.Add(-r.keep).UTC().Format(time.RFC3339)
		if _, ok := grouped[r.table]; !ok {
			order = append(order, r.table)
		}
		grouped[r.table] = append(grouped[r.table], clause{r.match, cut})
	}
	var total int64
	for _, table := range order {
		clauses := grouped[table]
		var b strings.Builder
		args := make([]any, 0, len(clauses))
		b.WriteString("SELECT COUNT(*) FROM (")
		for i, c := range clauses {
			if i > 0 {
				b.WriteString(" UNION ")
			}
			b.WriteString(c.match)
			// A match may repeat the same cutoff (same-pass "surviving rows" checks).
			for range strings.Count(c.match, "?") {
				args = append(args, c.cut)
			}
		}
		b.WriteString(")")
		var n int64
		if err := a.DB.QueryRowContext(ctx, b.String(), args...).Scan(&n); err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
