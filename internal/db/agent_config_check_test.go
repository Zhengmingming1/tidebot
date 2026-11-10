package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 045: runs get agent_config_changes; channels with the "needs you" group get
// the new agent-config notice once (re-running the insert adds nothing).
func TestAgentConfigCheckMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "045_")
	for _, q := range []string{
		`INSERT INTO notify_channels(id,kind,name,config_json,enabled,created_at) VALUES('c1','webhook','a','{}',1,'now'),('c2','webhook','b','{}',1,'now')`,
		`INSERT INTO notify_rules(id,event,task_id,channel_id,created_at) VALUES('r1','confirm_needed','','c1','now'),('r2','run_failed','','c2','now')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := database.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM notify_rules WHERE event='agent_config_changed' AND channel_id='c1' AND task_id=''`); n != 1 {
		t.Fatalf("c1 rules %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM notify_rules WHERE event='agent_config_changed' AND channel_id='c2'`); n != 0 {
		t.Fatalf("c2 rules %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM runs WHERE agent_config_changes<>''`); n != 0 {
		t.Fatal(n)
	}
	if n := count(`SELECT COUNT(*) FROM pending_confirmations`); n != 0 {
		t.Fatal("pending_confirmations table must stay")
	}
}
