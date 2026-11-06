package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPruneRowsKeepsRecentAndQueued(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	ago := func(d int) string { return now.Add(-time.Duration(d) * 24 * time.Hour).UTC().Format(time.RFC3339) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i, d := range []int{40, 40, 1} {
		exec(`INSERT INTO login_attempts(at, ip, ok, detail) VALUES(?,?,?, '')`, ago(d), fmt.Sprintf("10.0.0.%d", i), 0)
	}
	note := func(id, status, cleared string, d int) {
		exec(`INSERT INTO notifications(id, event, body, status, created_at, cleared_at) VALUES(?,?,?,?,?,?)`, id, "run_failed", "x", status, ago(d), cleared)
	}
	note("n_cleared_old", "logged", "nc_1", 40) // cleared, past 30 days: goes
	note("n_cleared_new", "logged", "nc_1", 10) // cleared, recent: stays (undo still works)
	note("n_shown_mid", "logged", "", 40)       // shown, under 180 days: stays
	note("n_shown_old", "sent", "", 200)        // shown, past 180 days: goes
	note("n_queued_old", "queued", "nc_2", 400) // still waiting for the digest: stays
	exec(`INSERT INTO budget_events(id, at, scope, kind, detail) VALUES('b_old', ?, 'global', 'exceeded', 'x')`, ago(400))
	exec(`INSERT INTO budget_events(id, at, scope, kind, detail) VALUES('b_new', ?, 'global', 'exceeded', 'x')`, ago(100))
	exec(`INSERT INTO runs(id, status, created_at) VALUES('r_ancient','success',?)`, ago(2000))
	// Workflow executions: large node outputs live in the DB; finished ones past keep go.
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('w_live','Live',1,0,?,?)`, ago(200), ago(200))
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('w_gone','Gone',1,1,?,?)`, ago(200), ago(200))
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_old_ok','w_live',1,'succeeded','','{}',?)`, ago(100))
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_new_ok','w_live',1,'succeeded','','{}',?)`, ago(10))
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_running','w_live',1,'running','','{}',?)`, ago(100))
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_queued','w_live',1,'queued','','{}',?)`, ago(100))
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_gone_mid','w_gone',1,'succeeded','','{}',?)`, ago(14)) // soft-deleted, past 7d
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_gone_new','w_gone',1,'failed','','{}',?)`, ago(2))     // soft-deleted, recent
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,output) VALUES('wr_old_ok','a','succeeded',?)`, strings.Repeat("x", 1024))
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,output) VALUES('wr_gone_mid','a','succeeded',?)`, strings.Repeat("y", 2048))
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,output) VALUES('wr_new_ok','a','succeeded','keep')`)

	n, err := a.pruneRows(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// 2 login + 1 cleared note + 1 old note + 1 budget + wr_old_ok + wr_gone_mid = 7
	if n != 7 {
		t.Fatalf("deleted %d rows, want 7", n)
	}
	ids := func(q string) string {
		rows, err := a.DB.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		return strings.Join(out, ",")
	}
	if got := ids(`SELECT id FROM notifications ORDER BY id`); got != "n_cleared_new,n_queued_old,n_shown_mid" {
		t.Fatalf("notifications left: %s", got)
	}
	if got := ids(`SELECT ip FROM login_attempts`); got != "10.0.0.2" {
		t.Fatalf("login attempts left: %s", got)
	}
	if got := ids(`SELECT id FROM budget_events`); got != "b_new" {
		t.Fatalf("budget events left: %s", got)
	}
	if got := ids(`SELECT id FROM runs`); got != "r_ancient" {
		t.Fatalf("run history must stay: %s", got)
	}
	if got := ids(`SELECT id FROM workflow_runs ORDER BY id`); got != "wr_gone_new,wr_new_ok,wr_queued,wr_running" {
		t.Fatalf("workflow runs left: %s", got)
	}
	if got := ids(`SELECT workflow_run_id||':'||node_id FROM workflow_run_nodes ORDER BY 1`); got != "wr_new_ok:a" {
		t.Fatalf("workflow nodes left: %s", got)
	}
	if n, _ := a.pruneRows(ctx, now); n != 0 {
		t.Fatalf("second pass deleted %d", n)
	}
}

func TestPruneRowsBatchesBigBacklog(t *testing.T) {
	a := newTestApp(t)
	old := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	total := pruneBatch*2 + 7
	for i := 0; i < total; i++ {
		if _, err := tx.Exec(`INSERT INTO login_attempts(at, ip, ok, detail) VALUES(?, '10.0.0.9', 0, '')`, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	n, err := a.pruneRows(context.Background(), time.Now())
	if err != nil || n != int64(total) {
		t.Fatalf("deleted %d (%v), want %d", n, err, total)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.pruneRows(ctx, time.Now()); err == nil {
		t.Fatal("canceled context should stop the pass")
	}
}

func TestCleanupRecordsPrunedRows(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.DB.Exec(`INSERT INTO login_attempts(at, ip, ok, detail) VALUES(?, '10.0.0.1', 0, '')`,
		time.Now().Add(-90*24*time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	a.retentionTick(ctx, time.Now()) // no files to delete, but a row went: recorded
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Last == nil || !v.Last.Auto || v.Last.DeletedRows != 1 || v.Last.DeletedFiles != 0 {
		t.Fatalf("last cleanup %+v", v.Last)
	}
}

func TestPruneQueriesUseIndexes(t *testing.T) {
	a := newTestApp(t)
	plans := map[string]string{
		`SELECT id FROM login_attempts WHERE at < '2026' LIMIT 1`:                                               "idx_login_attempts_at",
		`SELECT COUNT(1), MIN(at) FROM login_attempts WHERE ip='x' AND ok=0 AND at>='2026'`:                     "idx_login_attempts_ip_at",
		`SELECT id FROM notifications WHERE status <> 'queued' AND created_at < '2026' LIMIT 1`:                 "idx_notifications_created",
		`SELECT id FROM budget_events WHERE at < '2026' LIMIT 1`:                                                "idx_budget_events_at",
		`SELECT id FROM workflow_runs WHERE created_at < '2026' AND status NOT IN ('queued','running') LIMIT 1`: "idx_workflow_runs_created",
	}
	for q, idx := range plans {
		rows, err := a.DB.Query(`EXPLAIN QUERY PLAN ` + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			_ = rows.Scan(&id, &parent, &unused, &detail)
			plan = append(plan, detail)
		}
		rows.Close()
		if p := strings.Join(plan, "; "); !strings.Contains(p, idx) {
			t.Errorf("%s: plan %q doesn't use %s", q, p, idx)
		}
	}
}

func TestPruneWorkflowRunsReclaimsNodeOutput(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	ago := func(d int) string { return now.Add(-time.Duration(d) * 24 * time.Hour).UTC().Format(time.RFC3339) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('w1','W',1,0,?,?)`, ago(200), ago(200))
	const fat = 128 << 10 // MaxOutputBytes
	blob := strings.Repeat("z", fat)
	const nRuns = 20
	const nNodes = 8
	for i := 0; i < nRuns; i++ {
		rid := fmt.Sprintf("wr_%02d", i)
		exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES(?,?,1,'succeeded','','{}',?)`, rid, "w1", ago(120))
		for j := 0; j < nNodes; j++ {
			exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,output) VALUES(?,?, 'succeeded', ?)`, rid, fmt.Sprintf("n%d", j), blob)
		}
	}
	var beforeBytes int64
	if err := a.DB.QueryRow(`SELECT COALESCE(SUM(LENGTH(output)),0) FROM workflow_run_nodes`).Scan(&beforeBytes); err != nil {
		t.Fatal(err)
	}
	wantBytes := int64(nRuns * nNodes * fat)
	if beforeBytes != wantBytes {
		t.Fatalf("seeded %d bytes, want %d", beforeBytes, wantBytes)
	}
	var pageSize, pageCount, freelist int64
	_ = a.DB.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
	_ = a.DB.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
	_ = a.DB.QueryRow(`PRAGMA freelist_count`).Scan(&freelist)
	beforeFile := (pageCount) * pageSize
	n, err := a.pruneRows(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(nRuns) {
		t.Fatalf("deleted %d workflow runs, want %d", n, nRuns)
	}
	var afterBytes, afterNodes int64
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(LENGTH(output)),0), COUNT(*) FROM workflow_run_nodes`).Scan(&afterBytes, &afterNodes)
	if afterBytes != 0 || afterNodes != 0 {
		t.Fatalf("nodes left: count=%d bytes=%d", afterNodes, afterBytes)
	}
	_, _ = a.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_, err = a.DB.Exec(`VACUUM`)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.DB.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
	afterFile := pageCount * pageSize
	if afterFile >= beforeFile {
		t.Fatalf("db file did not shrink after vacuum: before=%d after=%d (payload was %d)", beforeFile, afterFile, beforeBytes)
	}
	t.Logf("workflow prune: removed %d runs / %d KB node output; db %d KB → %d KB after vacuum (−%d KB)",
		nRuns, beforeBytes>>10, beforeFile>>10, afterFile>>10, (beforeFile-afterFile)>>10)
}

func TestPruneSoftDeletedWorkflowVersionsReclaimsDefinition(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	ago := func(d int) string { return now.Add(-time.Duration(d) * 24 * time.Hour).UTC().Format(time.RFC3339) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Soft-deleted past the week, no runs left → versions + stub + bindings go.
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('wf_gone_old','GoneOld',2,1,?,?)`, ago(40), ago(14))
	fat := strings.Repeat(`{"nodes":[{"id":"n1","kind":"skill","name":"Step"}]}`+"x", 2000) // ~140 KB definition
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf_gone_old',1,?,?)`, fat, ago(40))
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf_gone_old',2,?,?)`, fat, ago(30))
	exec(`INSERT INTO capabilities(id,title,status,current_version_id,created_at,updated_at) VALUES('cap1','C','installed','v1',?,?)`, ago(40), ago(40))
	exec(`INSERT INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at) VALUES('workflow_node','wf_gone_old:n1','cap1','',?)`, ago(30))
	exec(`INSERT INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at) VALUES('task','task_live','cap1','',?)`, ago(30)) // unrelated: stays

	// Soft-deleted recently, or still has a run → keep versions.
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('wf_gone_new','GoneNew',1,1,?,?)`, ago(10), ago(2))
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf_gone_new',1,'{"nodes":[]}',?)`, ago(10))
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('wf_gone_run','GoneRun',1,1,?,?)`, ago(40), ago(20))
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf_gone_run',1,'{"nodes":[]}',?)`, ago(40))
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wr_keep','wf_gone_run',1,'succeeded','','{}',?)`, ago(2))

	// Live workflow: never touched by these rules.
	exec(`INSERT INTO workflows(id,name,version,deleted,created_at,updated_at) VALUES('wf_live','Live',1,0,?,?)`, ago(40), ago(40))
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf_live',1,'{"nodes":[]}',?)`, ago(40))

	var beforeDef int64
	if err := a.DB.QueryRow(`SELECT COALESCE(SUM(LENGTH(definition_json)),0) FROM workflow_versions WHERE workflow_id='wf_gone_old'`).Scan(&beforeDef); err != nil {
		t.Fatal(err)
	}
	if beforeDef < 100<<10 {
		t.Fatalf("fixture definition too small: %d", beforeDef)
	}
	var pageSize, pageCount int64
	_ = a.DB.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
	_ = a.DB.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
	beforeFile := pageCount * pageSize

	n, err := a.pruneRows(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// 2 versions + 1 binding + 1 workflow stub = 4 (wr_keep not old enough for runs rule)
	if n != 4 {
		t.Fatalf("deleted %d rows, want 4", n)
	}
	var leftVers, leftWF, leftBind, liveBind int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE workflow_id='wf_gone_old'`).Scan(&leftVers)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM workflows WHERE id='wf_gone_old'`).Scan(&leftWF)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM capability_bindings WHERE owner_id='wf_gone_old:n1'`).Scan(&leftBind)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM capability_bindings WHERE owner_id='task_live'`).Scan(&liveBind)
	if leftVers != 0 || leftWF != 0 || leftBind != 0 {
		t.Fatalf("soft-deleted reclaim left versions=%d workflows=%d bindings=%d", leftVers, leftWF, leftBind)
	}
	if liveBind != 1 {
		t.Fatal("unrelated task binding was removed")
	}
	keep := func(wid string) {
		t.Helper()
		var c int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE workflow_id=?`, wid).Scan(&c)
		if c != 1 {
			t.Fatalf("%s versions=%d, want 1", wid, c)
		}
	}
	keep("wf_gone_new")
	keep("wf_gone_run")
	keep("wf_live")

	_, _ = a.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if _, err := a.DB.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	_ = a.DB.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
	afterFile := pageCount * pageSize
	if afterFile >= beforeFile {
		t.Fatalf("db file did not shrink after vacuum: before=%d after=%d (payload %d)", beforeFile, afterFile, beforeDef)
	}
	t.Logf("soft-deleted workflow versions: removed 2 defs / %d KB; db %d KB → %d KB after vacuum (−%d KB)",
		beforeDef>>10, beforeFile>>10, afterFile>>10, (beforeFile-afterFile)>>10)
}
