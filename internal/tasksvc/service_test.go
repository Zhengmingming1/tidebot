package tasksvc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func TestTaskCRUDVersionRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ver := version.New(sqlDB)
	svc := New(store, ver, nil)
	ctx := context.Background()

	tv, err := svc.Create(ctx, CreateInput{Name: "n", Prompt: "p1", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if tv.CurrentVersion == nil || tv.CurrentVersion.VersionNo != 1 {
		t.Fatalf("%+v", tv.CurrentVersion)
	}
	spec := *tv.Spec
	spec.Prompt = "p2"
	tv2, err := svc.UpdateSpec(ctx, tv.Task.ID, spec, "user", "edit")
	if err != nil || tv2.CurrentVersion.VersionNo != 2 {
		t.Fatalf("%+v %v", tv2.CurrentVersion, err)
	}
	v1 := tv.CurrentVersion.ID
	if err := svc.Rollback(ctx, tv.Task.ID, v1, "user", "back"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, tv.Task.ID)
	if err != nil || got.CurrentVersion.ID != v1 || got.Spec.Prompt != "p1" {
		t.Fatalf("%+v", got)
	}
	if err := svc.SetStatus(ctx, tv.Task.ID, "paused", ""); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
}
