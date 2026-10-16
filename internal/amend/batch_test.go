package amend

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// fakeStructurer returns fixed items; "!" prefix = next_run, "?" prefix = conflict.
type fakeStructurer struct{}

func (fakeStructurer) Structure(_ context.Context, _ string, _ *tasksvc.Spec, u string) (StructuredProposal, error) {
	var p StructuredProposal
	for _, part := range strings.Split(u, ";") {
		it := ProposedItem{Op: "add", Scope: "permanent", Content: strings.TrimSpace(part)}
		if strings.HasPrefix(it.Content, "!") {
			it.Scope = "next_run"
		}
		if strings.HasPrefix(it.Content, "?") {
			it.Op = "modify"
			it.ConflictWith = []string{"c1"}
		}
		p.Items = append(p.Items, it)
	}
	return p, nil
}

type fixture struct {
	db    *sql.DB
	svc   *Service
	tasks *tasksvc.Service
	task  string
}

func newFixture(t *testing.T) (*fixture, context.Context) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	ver := version.New(sqlDB)
	tasks := tasksvc.New(db.NewStore(sqlDB), ver, nil)
	ctx := context.Background()
	tv, err := tasks.Create(ctx, tasksvc.CreateInput{Name: "t", Prompt: "do work", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{db: sqlDB, svc: New(sqlDB, tasks, ver, fakeStructurer{}), tasks: tasks, task: tv.Task.ID}, ctx
}

func (f *fixture) counts(t *testing.T) (versions, oplog int) {
	t.Helper()
	_ = f.db.QueryRow(`SELECT COUNT(1) FROM versions`).Scan(&versions)
	_ = f.db.QueryRow(`SELECT COUNT(1) FROM operation_log WHERE action='pointer_move'`).Scan(&oplog)
	return
}

func TestBatchCreatesExactlyOneVersion(t *testing.T) {
	f, ctx := newFixture(t)
	items, err := f.svc.Propose(ctx, f.task, "a;b;c;!d", "user")
	if err != nil || len(items) != 4 {
		t.Fatalf("%v %v", items, err)
	}
	v0, o0 := f.counts(t)
	res, err := f.svc.ConfirmBatch(ctx, items[0].MessageID, nil, "user")
	if err != nil {
		t.Fatal(err)
	}
	v1, o1 := f.counts(t)
	if v1-v0 != 1 || o1-o0 != 1 {
		t.Fatalf("want 1 version/1 oplog, got +%d/+%d", v1-v0, o1-o0)
	}
	if res.VersionID == "" || len(res.Items) != 4 {
		t.Fatalf("%+v", res)
	}
	view, _ := f.tasks.Get(ctx, f.task)
	if len(view.Spec.Constraints) != 3 || view.CurrentVersion.ID != res.VersionID {
		t.Fatalf("spec %+v", view.Spec.Constraints)
	}
	if res.Items[3].Status != "scheduled" {
		t.Fatalf("next-run item %+v", res.Items[3])
	}
}

func TestBatchPartialSelection(t *testing.T) {
	f, ctx := newFixture(t)
	items, _ := f.svc.Propose(ctx, f.task, "a;b;c", "user")
	v0, _ := f.counts(t)
	res, err := f.svc.ConfirmBatch(ctx, items[1].ID, []string{items[0].ID, items[2].ID}, "user")
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := f.counts(t)
	if v1-v0 != 1 || len(res.Items) != 2 || len(res.Excluded) != 1 {
		t.Fatalf("+%d %+v", v1-v0, res)
	}
	view, _ := f.tasks.Get(ctx, f.task)
	if len(view.Spec.Constraints) != 2 {
		t.Fatalf("%+v", view.Spec.Constraints)
	}
	left, _ := f.svc.List(ctx, f.task, "pending")
	if len(left) != 1 || left[0].ID != items[1].ID {
		t.Fatalf("excluded item should stay pending: %+v", left)
	}
}

func TestBatchBlockedByConflict(t *testing.T) {
	f, ctx := newFixture(t)
	items, _ := f.svc.Propose(ctx, f.task, "a;?b", "user")
	v0, _ := f.counts(t)
	if _, err := f.svc.ConfirmBatch(ctx, items[0].MessageID, nil, "user"); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict error, got %v", err)
	}
	if v1, _ := f.counts(t); v1 != v0 {
		t.Fatal("no version on blocked batch")
	}
	if it, _ := f.svc.get(ctx, items[0].ID); it.Status != "pending" {
		t.Fatalf("batch must be atomic: %+v", it)
	}
	if err := f.svc.ResolveConflict(ctx, items[1].ID, "coexist"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmBatch(ctx, items[0].MessageID, nil, "user"); err != nil {
		t.Fatal(err)
	}
	if v1, _ := f.counts(t); v1-v0 != 1 {
		t.Fatalf("+%d", v1-v0)
	}
}

func TestBatchAllNextRunNoVersion(t *testing.T) {
	f, ctx := newFixture(t)
	items, _ := f.svc.Propose(ctx, f.task, "!a;!b", "user")
	v0, o0 := f.counts(t)
	res, err := f.svc.ConfirmBatch(ctx, items[0].MessageID, nil, "user")
	if err != nil {
		t.Fatal(err)
	}
	v1, o1 := f.counts(t)
	if v1 != v0 || o1 != o0 || res.VersionID != "" {
		t.Fatalf("next-run-only must not create a version: +%d +%d %+v", v1-v0, o1-o0, res)
	}
	for _, it := range res.Items {
		if it.Status != "scheduled" {
			t.Fatalf("%+v", it)
		}
	}
}

func TestConfirmItemsMultipleOneVersion(t *testing.T) {
	f, ctx := newFixture(t)
	items, _ := f.svc.Propose(ctx, f.task, "a;b", "user")
	v0, _ := f.counts(t)
	if _, err := f.svc.ConfirmItems(ctx, []string{items[0].ID, items[1].ID}, "user"); err != nil {
		t.Fatal(err)
	}
	if v1, _ := f.counts(t); v1-v0 != 1 {
		t.Fatalf("+%d", v1-v0)
	}
}
