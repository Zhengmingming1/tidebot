package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 052 pads session times (RFC3339, whole seconds) to the fixed 9-digit layout
// a capability install already writes to chat_sessions.updated_at, so the
// session list orders by time inside one second.
func TestChatSessionFixedWidthTimesMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "052_")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// chat_a: touched at 01:02:05 (RFC3339); chat_b: a capability install at 01:02:05.4
	exec(`INSERT INTO chat_sessions(id,created_at,updated_at) VALUES('chat_a','2026-11-20T01:02:03Z','2026-11-20T01:02:05Z'),('chat_b','2026-11-20T01:02:04Z','2026-11-20T01:02:05.400000000Z'),('chat_odd','x','')`)
	exec(`INSERT INTO chat_session_turns(session_id,turn_index,run_id,created_at) VALUES('chat_a',0,'run_a','2026-11-20T01:02:03Z'),('chat_a',1,'run_b','2026-11-20T01:02:05.12Z')`)
	exec(`INSERT INTO capabilities(id,title,current_version_id,created_at,updated_at) VALUES('cap','Cap','v1','t','t')`)
	exec(`INSERT INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at) VALUES('session','chat_a','cap','','2026-11-20T01:02:03Z')`)
	one := func(q string) string {
		t.Helper()
		var v string
		if err := database.QueryRow(q).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	list := `SELECT group_concat(id) FROM (SELECT id FROM chat_sessions WHERE id<>'chat_odd' ORDER BY updated_at DESC, id DESC)`
	if got := one(list); got != "chat_a,chat_b" {
		t.Fatalf("old rows should show the bug: %s", got)
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		`SELECT created_at FROM chat_sessions WHERE id='chat_a'`:             "2026-11-20T01:02:03.000000000Z",
		`SELECT updated_at FROM chat_sessions WHERE id='chat_a'`:             "2026-11-20T01:02:05.000000000Z",
		`SELECT updated_at FROM chat_sessions WHERE id='chat_b'`:             "2026-11-20T01:02:05.400000000Z",
		`SELECT created_at FROM chat_sessions WHERE id='chat_odd'`:           "x",
		`SELECT updated_at FROM chat_sessions WHERE id='chat_odd'`:           "",
		`SELECT created_at FROM chat_session_turns WHERE turn_index=0`:       "2026-11-20T01:02:03.000000000Z",
		`SELECT created_at FROM chat_session_turns WHERE turn_index=1`:       "2026-11-20T01:02:05.120000000Z",
		`SELECT COUNT(*) FROM capability_bindings WHERE owner_id='chat_a'`:   "1",
		`SELECT created_at FROM capability_bindings WHERE owner_id='chat_a'`: "2026-11-20T01:02:03Z",
	}
	for q, w := range want {
		if got := one(q); got != w {
			t.Errorf("%s: got %q want %q", q, got, w)
		}
	}
	if got := one(list); got != "chat_b,chat_a" {
		t.Fatalf("order after migration: %s", got)
	}
}
