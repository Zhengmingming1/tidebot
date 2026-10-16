package amend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func TestProposeConfirmPermanentAndNextRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ver := version.New(sqlDB)
	tasks := tasksvc.New(store, ver, nil)
	svc := New(sqlDB, tasks, ver, MockStructurer{})
	ctx := context.Background()

	tv, err := tasks.Create(ctx, tasksvc.CreateInput{Name: "t", Prompt: "do work", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.Propose(ctx, tv.Task.ID, "always include a summary section", "user")
	if err != nil || len(items) != 1 || items[0].Status != "pending" {
		t.Fatalf("%+v %v", items, err)
	}
	got, err := svc.Confirm(ctx, items[0].ID, "user")
	if err != nil || got.Status != "applied" {
		t.Fatalf("%+v %v", got, err)
	}
	view, _ := tasks.Get(ctx, tv.Task.ID)
	if view.CurrentVersion.VersionNo < 2 {
		t.Fatalf("expected new version, got %+v", view.CurrentVersion)
	}
	if len(view.Spec.Constraints) != 1 {
		t.Fatalf("constraints=%+v", view.Spec.Constraints)
	}

	items2, err := svc.Propose(ctx, tv.Task.ID, "only this run skip email", "user")
	if err != nil {
		t.Fatal(err)
	}
	got2, err := svc.Confirm(ctx, items2[0].ID, "user")
	if err != nil || got2.Status != "scheduled" {
		t.Fatalf("%+v %v", got2, err)
	}
	prompt, _, ids, err := tasks.CompilePrompt(ctx, tv.Task.ID)
	if err != nil || len(ids) != 1 || !contains(prompt, "skip email") {
		t.Fatalf("prompt=%q ids=%v err=%v", prompt, ids, err)
	}
}

func TestConflictMustResolve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ver := version.New(sqlDB)
	tasks := tasksvc.New(store, ver, nil)
	svc := New(sqlDB, tasks, ver, MockStructurer{})
	ctx := context.Background()
	tv, _ := tasks.Create(ctx, tasksvc.CreateInput{Name: "t", Prompt: "base", Actor: "user"})
	// seed a constraint
	spec := *tv.Spec
	spec.Constraints = append(spec.Constraints, tasksvc.SpecConstraint{ID: "c1", Text: "old"})
	_, _ = tasks.UpdateSpec(ctx, tv.Task.ID, spec, "user", "seed")
	items, err := svc.Propose(ctx, tv.Task.ID, "replace the first constraint with new rule", "user")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "needs_decision" {
		t.Fatalf("%+v", items[0])
	}
	if _, err := svc.Confirm(ctx, items[0].ID, "user"); err == nil {
		t.Fatal("expected confirm blocked")
	}
	if err := svc.ResolveConflict(ctx, items[0].ID, "replace"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, items[0].ID, "user"); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || stringIndex(s, sub) >= 0)
}
func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
