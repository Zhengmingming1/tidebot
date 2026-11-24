package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// 051 pads capability timestamps (RFC3339Nano, and RFC3339 for session-default
// events) to a fixed 9-digit fraction so text ordering matches time order. The
// append-only guards on versions and events must still be in place afterwards.
func TestCapabilityFixedWidthTimesMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "051_")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO capabilities(id,title,current_version_id,created_at,updated_at) VALUES('cap','Cap','v2','2026-11-20T01:02:03Z','2026-11-20T01:02:05.3Z')`)
	exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES('v1','cap','{"installed_at":"2026-11-20T01:02:05Z"}','abc','{}','2026-11-20T01:02:05Z'),('v2','cap','{}','def','{}','2026-11-20T01:02:05.3Z')`)
	// a session-default event (RFC3339) written just before an install in the same second
	exec(`INSERT INTO capability_events(id,capability_id,version_id,actor,action,result,created_at) VALUES('e_whole','cap','v1','ui','session_default','enabled','2026-11-20T01:02:05Z'),('e_frac','cap','v2','ui','install','installed','2026-11-20T01:02:05.3Z'),('e_odd','cap','','ui','x','y','t')`)
	exec(`INSERT INTO capability_proposals(id,session_id,run_id,kind,payload_json,status,decided_at,created_at,expires_at) VALUES('p1','s','','install','{}','applied','2026-11-20T01:02:06.12Z','2026-11-20T01:02:05Z','2026-11-21T01:02:05Z'),('p2','s','','install','{}','pending','','2026-11-20T01:02:05.123456789Z','2026-11-21T01:02:05.123456789Z')`)
	order := func(q string) string {
		t.Helper()
		var v string
		if err := database.QueryRow(q).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	versions := `SELECT group_concat(id) FROM (SELECT id FROM capability_versions ORDER BY created_at DESC,id DESC)`
	events := `SELECT group_concat(id) FROM (SELECT id FROM capability_events WHERE id<>'e_odd' ORDER BY created_at DESC,id DESC)`
	proposals := `SELECT group_concat(id) FROM (SELECT id FROM capability_proposals ORDER BY created_at DESC)`
	if v, e, p := order(versions), order(events), order(proposals); v != "v1,v2" || e != "e_whole,e_frac" || p != "p1,p2" {
		t.Fatalf("old rows should show the bug: %s / %s / %s", v, e, p)
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		`SELECT created_at FROM capabilities`:                         "2026-11-20T01:02:03.000000000Z",
		`SELECT updated_at FROM capabilities`:                         "2026-11-20T01:02:05.300000000Z",
		`SELECT created_at FROM capability_versions WHERE id='v1'`:    "2026-11-20T01:02:05.000000000Z",
		`SELECT lock_json FROM capability_versions WHERE id='v1'`:     `{"installed_at":"2026-11-20T01:02:05Z"}`,
		`SELECT lock_sha256 FROM capability_versions WHERE id='v1'`:   "abc",
		`SELECT created_at FROM capability_events WHERE id='e_whole'`: "2026-11-20T01:02:05.000000000Z",
		`SELECT created_at FROM capability_events WHERE id='e_odd'`:   "t",
		`SELECT decided_at FROM capability_proposals WHERE id='p1'`:   "2026-11-20T01:02:06.120000000Z",
		`SELECT expires_at FROM capability_proposals WHERE id='p1'`:   "2026-11-21T01:02:05.000000000Z",
		`SELECT decided_at FROM capability_proposals WHERE id='p2'`:   "",
		`SELECT created_at FROM capability_proposals WHERE id='p2'`:   "2026-11-20T01:02:05.123456789Z",
	}
	for q, w := range want {
		if got := order(q); got != w {
			t.Errorf("%s: got %q want %q", q, got, w)
		}
	}
	if v, e, p := order(versions), order(events), order(proposals); v != "v2,v1" || e != "e_frac,e_whole" || p != "p2,p1" {
		t.Fatalf("order after migration: %s / %s / %s", v, e, p)
	}
	for _, q := range []string{
		`UPDATE capability_versions SET created_at='x' WHERE id='v1'`,
		`UPDATE capability_events SET created_at='x' WHERE id='e_whole'`,
		`DELETE FROM capability_events WHERE id='e_whole'`,
	} {
		if _, err := database.Exec(q); err == nil || !strings.Contains(err.Error(), "capability") {
			t.Errorf("%s: guard trigger missing after migration (err=%v)", q, err)
		}
	}
}
