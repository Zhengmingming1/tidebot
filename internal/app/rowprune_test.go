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

	n, err := a.pruneRows(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("deleted %d rows, want 5", n)
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
		`SELECT id FROM login_attempts WHERE at < '2026' LIMIT 1`:                               "idx_login_attempts_at",
		`SELECT COUNT(1), MIN(at) FROM login_attempts WHERE ip='x' AND ok=0 AND at>='2026'`:     "idx_login_attempts_ip_at",
		`SELECT id FROM notifications WHERE status <> 'queued' AND created_at < '2026' LIMIT 1`: "idx_notifications_created",
		`SELECT id FROM budget_events WHERE at < '2026' LIMIT 1`:                                "idx_budget_events_at",
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
