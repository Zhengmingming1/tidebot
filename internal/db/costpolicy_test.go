package db

import (
	"database/sql"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLegacyCostPolicyMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	entries, _ := migrationFS.ReadDir("migrations")
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i, name := range names {
		if strings.HasPrefix(name, "034_") {
			break
		}
		raw, _ := migrationFS.ReadFile("migrations/" + name)
		if _, err = database.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err = database.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,'now')`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"high", "low", "none"} {
		if _, err = database.Exec(`INSERT INTO tasks(id,name,created_at,updated_at) VALUES(?,?,'now','now')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = database.Exec(`INSERT INTO settings(user_id,key,value_json) VALUES('owner','pricing.fx','{"display":"CNY","usd_cny":8}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`INSERT INTO budgets(id,scope,scope_id,period,max_usd,max_tokens,action,warn_at_pct,created_at,updated_at,currency) VALUES
 ('h','task','high','run',8,321,'notify',75,'now','now','CNY'),
 ('l','task','low','run',0.01,NULL,'pause_notify',80,'now','now','USD')`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(database); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id       string
		amount   float64
		currency string
	}{{"high", .4, "CNY"}, {"low", .01, "USD"}} {
		var amount float64
		var currency, mode string
		if err = database.QueryRow(`SELECT b.max_usd,b.currency,t.cost_control_mode FROM budgets b JOIN tasks t ON t.id=b.scope_id WHERE b.scope_id=? AND b.period='run'`, tt.id).Scan(&amount, &currency, &mode); err != nil {
			t.Fatal(err)
		}
		if math.Abs(amount-tt.amount) > 1e-10 || currency != tt.currency || mode != "task" {
			t.Fatalf("%s: %v %s %s", tt.id, amount, currency, mode)
		}
	}
	// 044: the cap 034 filled in for "none" was tidebot's, not the owner's: removed
	// (and logged); "high" was lowered from the owner's own cap and stays.
	var left int
	database.QueryRow(`SELECT COUNT(*) FROM budgets WHERE scope_id='none' AND period='run'`).Scan(&left)
	if left != 0 {
		t.Fatal("implicit default cap kept")
	}
	var logged float64
	if err = database.QueryRow(`SELECT json_extract(detail_json,'$.max_amount') FROM operation_log WHERE entity_id='none' AND action='unlimited_default_migration'`).Scan(&logged); err != nil || math.Abs(logged-.4) > 1e-10 {
		t.Fatalf("removal not logged %v %v", logged, err)
	}
	var original float64
	var tokens, warn int
	var action string
	if err = database.QueryRow(`SELECT json_extract(detail_json,'$.max_amount') FROM operation_log WHERE entity_id='high' AND action='cost_policy_migration'`).Scan(&original); err != nil || original != 8 {
		t.Fatalf("audit %v %v", original, err)
	}
	database.QueryRow(`SELECT max_tokens,action,warn_at_pct FROM budgets WHERE id='h'`).Scan(&tokens, &action, &warn)
	if tokens != 321 || action != "notify" || warn != 75 {
		t.Fatal("lost legacy budget metadata")
	}
	if err = Migrate(database); err != nil {
		t.Fatal("migration not repeatable", err)
	}
}
