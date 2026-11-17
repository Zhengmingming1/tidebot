package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Old workflow runs did not store the starting task; 048 fills it from a child
// node run that already has task_id, and leaves hand-started runs empty.
func TestWorkflowRunTaskBackfill(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "048_")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tasks(id,name,created_at,updated_at) VALUES('tsk_night','Nightly','t','t')`)
	exec(`INSERT INTO workflows(id,name,version,created_at,updated_at) VALUES('wf1','Flow',1,'t','t')`)
	exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf1',1,'{}','t')`)
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wfr_task','wf1',1,'succeeded','','{}','t')`)
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wfr_hand','wf1',1,'succeeded','','{}','t')`)
	exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('wfr_empty','wf1',1,'failed','','{}','t')`)
	exec(`INSERT INTO runs(id,task_id,status,prompt,created_at) VALUES('run_n','tsk_night','succeeded','','t')`)
	exec(`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_h','succeeded','','t')`)
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,child_run_id) VALUES('wfr_task','a','succeeded','run_n')`)
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,child_run_id) VALUES('wfr_hand','a','succeeded','run_h')`)
	exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,child_run_id) VALUES('wfr_empty','a','failed','')`)
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, err := database.Query(`SELECT id, task_id FROM workflow_runs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, task string
		if err := rows.Scan(&id, &task); err != nil {
			t.Fatal(err)
		}
		got[id] = task
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got["wfr_task"] != "tsk_night" || got["wfr_hand"] != "" || got["wfr_empty"] != "" {
		t.Fatalf("backfill: %v", got)
	}
}
