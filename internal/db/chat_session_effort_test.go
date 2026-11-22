package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Sessions saved before 049 used the effort of the run they continue; the
// migration copies a valid value over and leaves blank sessions, runs without
// one, missing runs and invalid values at "" (default).
func TestChatSessionEffortBackfill(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "049_")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO runs(id,status,prompt,created_at,effort) VALUES('run_hi','succeeded','','t','high')`)
	exec(`INSERT INTO runs(id,status,prompt,created_at,effort) VALUES('run_def','succeeded','','t','')`)
	exec(`INSERT INTO runs(id,status,prompt,created_at,effort) VALUES('run_bad','succeeded','','t','max')`)
	for _, row := range [][2]string{{"chat_hi", "run_hi"}, {"chat_def", "run_def"}, {"chat_bad", "run_bad"}, {"chat_gone", "run_gone"}, {"chat_blank", ""}} {
		exec(`INSERT INTO chat_sessions(id,origin_run_id,created_at,updated_at) VALUES(?,?,'t','t')`, row[0], row[1])
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, err := database.Query(`SELECT id, effort FROM chat_sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, e string
		if err := rows.Scan(&id, &e); err != nil {
			t.Fatal(err)
		}
		got[id] = e
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"chat_hi": "high", "chat_def": "", "chat_bad": "", "chat_gone": "", "chat_blank": ""}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("backfill: %v", got)
		}
	}
}
