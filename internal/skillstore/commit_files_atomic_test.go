package skillstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// A skill version and its file list are one write: before, a failed file-list
// insert was ignored and the new (current) version staged as an empty skill.
func TestCommitFilesIsAtomic(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ver := version.New(sqlDB)
	svc := New(sqlDB, ver, tasksvc.New(db.NewStore(sqlDB), ver, nil), "")
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sk, err := svc.ImportDir(ctx, "hello", dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	v1 := sk.CurrentVersion.ID

	if _, err := sqlDB.Exec(`CREATE TRIGGER no_files BEFORE INSERT ON version_files BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	v2files := map[string][]byte{"SKILL.md": []byte("# v2\n")}
	if _, _, err := svc.CommitFiles(ctx, sk.ID, "hello", v2files, "edit", "user", false); err == nil {
		t.Fatal("commit succeeded with the file list failing")
	}
	cur, err := ver.CurrentVersion(ctx, version.EntitySkill, sk.ID)
	if err != nil || cur.ID != v1 {
		t.Fatalf("current moved to a version without files: %v %v", cur, err)
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM versions WHERE entity_id=?`, sk.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("versions after failed commit: %d %v", n, err)
	}

	if _, err := sqlDB.Exec(`DROP TRIGGER no_files`); err != nil {
		t.Fatal(err)
	}
	v2, _, err := svc.CommitFiles(ctx, sk.ID, "hello", v2files, "edit", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	files, err := svc.Files(ctx, v2.ID)
	if err != nil || string(files["SKILL.md"]) != "# v2\n" {
		t.Fatalf("files of the new version: %q %v", files, err)
	}
}
