package db

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// migrateBefore applies the migrations that sort before prefix (as an old install had them).
func migrateBefore(t *testing.T, database *sql.DB, prefix string) {
	t.Helper()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrationFS.ReadDir("migrations")
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i, name := range names {
		if strings.HasPrefix(name, prefix) {
			return
		}
		raw, _ := migrationFS.ReadFile("migrations/" + name)
		if _, err := database.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := database.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,'now')`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("no migration %s", prefix)
}

func TestUnlimitedDefaultsMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "044_")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"plain", "saved", "edited", "custom", "tokens", "global", "cny", "cnyold"} {
		exec(`INSERT INTO tasks(id,name,created_at,updated_at) VALUES(?,?,'now','now')`, id, id)
	}
	exec(`UPDATE tasks SET cost_control_mode='global' WHERE id='global'`)
	exec(`INSERT INTO settings(user_id,key,value_json) VALUES('owner','pricing.fx','{"display":"USD","usd_cny":7}')`)
	row := func(id string, amount float64, cur string, tokens any) {
		exec(`INSERT INTO budgets(id,scope,scope_id,period,max_usd,max_tokens,action,warn_at_pct,bucket,created_at,updated_at,currency) VALUES(?,'task',?,'run',?,?,'pause_notify',80,'','now','now',?)`, "b-"+id, id, amount, tokens, cur)
	}
	row("plain", 0.05, "USD", nil)  // filled in by tidebot, never edited
	row("saved", 0.05, "USD", nil)  // form saved with the prefilled value
	row("edited", 0.05, "USD", nil) // owner changed it and came back to 0.05
	row("custom", 0.2, "USD", nil)  // owner's own cap
	row("tokens", 0.05, "USD", 500) // a token cap on the row
	row("global", 0.05, "USD", nil) // not using task limits
	row("cny", 0.35, "CNY", nil)    // 0.05 x 7
	row("cnyold", 0.4, "CNY", nil)  // CNY default at an older rate: not provably ours
	log := func(id, before, after string) {
		exec(`INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES('now','task',?,'cost_policy_update','webui',?)`, id, `{"Before":{"mode":"task","run_amount":`+before+`},"After":{"mode":"task","run_amount":`+after+`}}`)
	}
	log("saved", "0.05", "0.05")
	log("edited", "0.05", "0.1")
	log("edited", "0.1", "0.05")
	exec(`UPDATE tasks SET cost_policy_revision=3 WHERE id='edited'`)
	if err = Migrate(database); err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{"plain": true, "saved": true, "cny": true}
	for _, id := range []string{"plain", "saved", "edited", "custom", "tokens", "global", "cny", "cnyold"} {
		var n, logged, rev int
		database.QueryRow(`SELECT COUNT(*) FROM budgets WHERE scope_id=? AND period='run'`, id).Scan(&n)
		database.QueryRow(`SELECT COUNT(*) FROM operation_log WHERE entity_id=? AND action='unlimited_default_migration'`, id).Scan(&logged)
		database.QueryRow(`SELECT cost_policy_revision FROM tasks WHERE id=?`, id).Scan(&rev)
		if removed[id] != (n == 0) || removed[id] != (logged == 1) {
			t.Fatalf("%s: rows=%d logged=%d", id, n, logged)
		}
		if removed[id] && rev != 2 {
			t.Fatalf("%s: revision %d not bumped", id, rev)
		}
	}
	var amount float64
	var cur string
	if err = database.QueryRow(`SELECT json_extract(detail_json,'$.max_amount'),json_extract(detail_json,'$.currency') FROM operation_log WHERE entity_id='cny' AND action='unlimited_default_migration'`).Scan(&amount, &cur); err != nil || amount != 0.35 || cur != "CNY" {
		t.Fatalf("original not kept: %v %s %v", amount, cur, err)
	}
	if err = Migrate(database); err != nil {
		t.Fatal("not repeatable", err)
	}
}
