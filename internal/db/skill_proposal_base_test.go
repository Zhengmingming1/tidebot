package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 046: skill_proposals get base_version_id; existing rows take the parent of
// the proposed version (Commit set that to current when the proposal was made).
func TestSkillProposalBaseVersionMigration(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrateBefore(t, database, "046_")
	for _, q := range []string{
		`INSERT INTO skills(id,name,created_at) VALUES('sk1','backup','now')`,
		`INSERT INTO versions(id,entity_type,entity_id,version_no,content_hash,source,created_at) VALUES('ver_1','skill','sk1',1,'h1','import','now')`,
		`INSERT INTO versions(id,entity_type,entity_id,version_no,content_hash,parent_version_id,source,created_at) VALUES('ver_2','skill','sk1',2,'h2','ver_1','optimization','now')`,
		`INSERT INTO versions(id,entity_type,entity_id,version_no,content_hash,source,created_at) VALUES('ver_orphan','skill','sk1',3,'h3','optimization','now')`,
		`INSERT INTO skill_proposals(id,skill_id,version_id,source,status,created_at) VALUES('prop_ok','sk1','ver_2','user','pending','now')`,
		`INSERT INTO skill_proposals(id,skill_id,version_id,source,status,created_at) VALUES('prop_none','sk1','ver_orphan','auto','pending','now')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	base := func(id string) string {
		t.Helper()
		var b string
		if err := database.QueryRow(`SELECT base_version_id FROM skill_proposals WHERE id=?`, id).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if got := base("prop_ok"); got != "ver_1" {
		t.Fatalf("prop_ok base=%q want ver_1", got)
	}
	if got := base("prop_none"); got != "" {
		t.Fatalf("prop_none base=%q want empty", got)
	}
}
