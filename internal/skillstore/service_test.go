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

func TestImportPinMaterialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ver := version.New(sqlDB)
	tasks := tasksvc.New(store, ver, nil)
	svc := New(sqlDB, ver, tasks, "")
	ctx := context.Background()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Hello\nDo the thing.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sk, err := svc.ImportDir(ctx, "hello", dir, "user")
	if err != nil || sk.CurrentVersion == nil {
		t.Fatalf("%+v %v", sk, err)
	}
	tv, err := tasks.Create(ctx, tasksvc.CreateInput{Name: "t", Prompt: "run", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.PinOnTask(ctx, tv.Task.ID, sk.ID, sk.CurrentVersion.ID, "user"); err != nil {
		t.Fatal(err)
	}
	view, _ := tasks.Get(ctx, tv.Task.ID)
	if len(view.Spec.SkillBindings) != 1 || view.Spec.SkillBindings[0].Mode != "pinned" {
		t.Fatalf("%+v", view.Spec)
	}
	ws := t.TempDir()
	if err := svc.Materialize(ctx, ws, view.Spec.SkillBindings); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(ws, ".claude", "skills", "hello", "SKILL.md"))
	if err != nil || string(got) == "" {
		t.Fatal(err)
	}
	// rollback skill after a second import
	dir2 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir2, "SKILL.md"), []byte("# Hello v2\n"), 0o600)
	sk2, err := svc.ImportDir(ctx, "hello", dir2, "user")
	if err != nil || sk2.CurrentVersion.VersionNo < 2 {
		t.Fatalf("%+v %v", sk2, err)
	}
	if err := svc.Rollback(ctx, sk.ID, sk.CurrentVersion.ID, "user"); err != nil {
		t.Fatal(err)
	}
	cur, _ := ver.CurrentVersion(ctx, version.EntitySkill, sk.ID)
	if cur.ID != sk.CurrentVersion.ID {
		t.Fatalf("rollback failed %+v", cur)
	}
}
