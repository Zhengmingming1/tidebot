package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 050 pads workflow timestamps written with RFC3339Nano (trailing zeros dropped)
// to a fixed 9-digit fraction, so text ordering matches time order. Empty and
// odd values are left alone.
func TestWorkflowFixedWidthTimesMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "050_")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workflows(id,name,version,created_at,updated_at) VALUES('wf1','Flow',1,'2026-11-20T01:02:03Z','2026-11-20T01:02:03.5Z')`)
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf1',1,'{}','2026-11-20T01:02:03.12Z')`)
	// a whole second sorted after a later time in the same second before the fix
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at,started_at,finished_at) VALUES('wfr_whole','wf1',1,'succeeded','','{}','2026-11-20T01:02:05Z','2026-11-20T01:02:05.1Z','')`)
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at,started_at,finished_at) VALUES('wfr_frac','wf1',1,'succeeded','','{}','2026-11-20T01:02:05.3Z','2026-11-20T01:02:04.123456789Z','2026-11-20T01:02:04.99999999Z')`)
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wfr_odd','wf1',1,'failed','','{}','t')`)
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,started_at,finished_at) VALUES('wfr_whole','a','succeeded','2026-11-20T01:02:05.1Z','2026-11-20T01:02:06Z')`)
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,started_at,finished_at) VALUES('wfr_odd','a','pending','','2026-11-20 01:02:06')`)
	order := func() string {
		t.Helper()
		var v string
		if err := database.QueryRow(`SELECT group_concat(id) FROM (SELECT id FROM workflow_runs WHERE id<>'wfr_odd' ORDER BY created_at DESC,id DESC)`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := order(); got != "wfr_whole,wfr_frac" {
		t.Fatalf("old rows should show the bug: %s", got)
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	one := func(q string) string {
		t.Helper()
		var v string
		if err := database.QueryRow(q).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	want := map[string]string{
		`SELECT created_at FROM workflows`:                                             "2026-11-20T01:02:03.000000000Z",
		`SELECT updated_at FROM workflows`:                                             "2026-11-20T01:02:03.500000000Z",
		`SELECT created_at FROM workflow_versions`:                                     "2026-11-20T01:02:03.120000000Z",
		`SELECT created_at FROM workflow_runs WHERE id='wfr_whole'`:                    "2026-11-20T01:02:05.000000000Z",
		`SELECT started_at FROM workflow_runs WHERE id='wfr_whole'`:                    "2026-11-20T01:02:05.100000000Z",
		`SELECT finished_at FROM workflow_runs WHERE id='wfr_whole'`:                   "",
		`SELECT created_at FROM workflow_runs WHERE id='wfr_frac'`:                     "2026-11-20T01:02:05.300000000Z",
		`SELECT started_at FROM workflow_runs WHERE id='wfr_frac'`:                     "2026-11-20T01:02:04.123456789Z",
		`SELECT finished_at FROM workflow_runs WHERE id='wfr_frac'`:                    "2026-11-20T01:02:04.999999990Z",
		`SELECT created_at FROM workflow_runs WHERE id='wfr_odd'`:                      "t",
		`SELECT started_at FROM workflow_run_nodes WHERE workflow_run_id='wfr_whole'`:  "2026-11-20T01:02:05.100000000Z",
		`SELECT finished_at FROM workflow_run_nodes WHERE workflow_run_id='wfr_whole'`: "2026-11-20T01:02:06.000000000Z",
		`SELECT started_at FROM workflow_run_nodes WHERE workflow_run_id='wfr_odd'`:    "",
		`SELECT finished_at FROM workflow_run_nodes WHERE workflow_run_id='wfr_odd'`:   "2026-11-20 01:02:06",
	}
	for q, w := range want {
		if got := one(q); got != w {
			t.Errorf("%s: got %q want %q", q, got, w)
		}
	}
	if got := order(); got != "wfr_frac,wfr_whole" {
		t.Fatalf("order after migration: %s", got)
	}
}
