package tasksvc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func atomicFixture(t *testing.T) (*Service, context.Context) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return New(db.NewStore(sqlDB), version.New(sqlDB), nil), context.Background()
}

// failWith makes statements matching the trigger body fail like a busy or full
// database would, until the returned func drops the trigger.
func failWith(t *testing.T, s *Service, name, on string) func() {
	t.Helper()
	if _, err := s.Store.DB.Exec(`CREATE TRIGGER ` + name + ` BEFORE ` + on + ` BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := s.Store.DB.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, s *Service, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Store.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A task row never exists without a version: before, a failed first commit left
// a task that was listed (and scheduled) but could not be opened or edited.
func TestCreateTaskIsAtomic(t *testing.T) {
	s, ctx := atomicFixture(t)
	drop := failWith(t, s, "no_ptr", `INSERT ON pointers`)
	if _, err := s.Create(ctx, CreateInput{Name: "n", Prompt: "p", CronExpr: "*/5 * * * *"}); err == nil {
		t.Fatal("create succeeded with pointer writes failing")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM tasks`); n != 0 {
		t.Fatalf("tasks left behind: %d", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM versions`); n != 0 {
		t.Fatalf("versions left behind: %d", n)
	}
	drop()
	tv, err := s.Create(ctx, CreateInput{Name: "n", Prompt: "p"})
	if err != nil || tv.CurrentVersion == nil || tv.CurrentVersion.VersionNo != 1 {
		t.Fatalf("%+v %v", tv.CurrentVersion, err)
	}
}

// A failed save changes neither the version nor the row, and saving again with
// the same expected version is not refused as a conflict.
func TestUpdateSpecIsAtomic(t *testing.T) {
	s, ctx := atomicFixture(t)
	tv, err := s.Create(ctx, CreateInput{Name: "n", Prompt: "p1", WorkspaceDir: "/old"})
	if err != nil {
		t.Fatal(err)
	}
	v1 := tv.CurrentVersion.ID
	spec := *tv.Spec
	spec.Prompt, spec.WorkspaceDir = "p2", ""
	drop := failWith(t, s, "no_sync", `UPDATE OF prompt ON tasks`)
	if _, err := s.UpdateSpecExpected(ctx, tv.Task.ID, spec, "user", "edit", v1); err == nil {
		t.Fatal("save succeeded with the row sync failing")
	}
	got, err := s.Get(ctx, tv.Task.ID)
	if err != nil || got.CurrentVersion.ID != v1 || got.Spec.Prompt != "p1" || got.Task.WorkspaceDir != "/old" {
		t.Fatalf("half saved: version %s prompt %q row dir %q", got.CurrentVersion.ID, got.Spec.Prompt, got.Task.WorkspaceDir)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM versions WHERE entity_id=?`, tv.Task.ID); n != 1 {
		t.Fatalf("versions after failed save: %d", n)
	}
	drop()
	tv2, err := s.UpdateSpecExpected(ctx, tv.Task.ID, spec, "user", "edit", v1)
	if errors.Is(err, ErrConflict) || err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ = s.Get(ctx, tv.Task.ID)
	if got.CurrentVersion.ID != tv2.CurrentVersion.ID || got.Task.Prompt != "p2" || got.Task.WorkspaceDir != "" {
		t.Fatalf("after retry: %+v", got.Task)
	}
}

// Rollback moves the pointer and the row together; before, a failed row sync was
// ignored and runs kept using the newer version's folder.
func TestRollbackIsAtomic(t *testing.T) {
	s, ctx := atomicFixture(t)
	tv, err := s.Create(ctx, CreateInput{Name: "n", Prompt: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	v1 := tv.CurrentVersion.ID
	spec := *tv.Spec
	spec.Prompt, spec.WorkspaceDir = "p2", "/new"
	tv2, err := s.UpdateSpec(ctx, tv.Task.ID, spec, "user", "edit")
	if err != nil {
		t.Fatal(err)
	}
	drop := failWith(t, s, "no_sync", `UPDATE OF workspace_dir ON tasks`)
	if err := s.Rollback(ctx, tv.Task.ID, v1, "user", "back"); err == nil {
		t.Fatal("rollback succeeded with the row sync failing")
	}
	got, _ := s.Get(ctx, tv.Task.ID)
	if got.CurrentVersion.ID != tv2.CurrentVersion.ID || got.Task.WorkspaceDir != "/new" {
		t.Fatalf("half rolled back: version %s row dir %q", got.CurrentVersion.ID, got.Task.WorkspaceDir)
	}
	drop()
	if err := s.Rollback(ctx, tv.Task.ID, v1, "user", "back"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, tv.Task.ID)
	if got.CurrentVersion.ID != v1 || got.Task.Prompt != "p1" || got.Task.WorkspaceDir != "" {
		t.Fatalf("after rollback: version %s %+v", got.CurrentVersion.ID, got.Task)
	}
}

func TestDuplicateTaskIsAtomic(t *testing.T) {
	s, ctx := atomicFixture(t)
	tv, err := s.Create(ctx, CreateInput{Name: "n", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	drop := failWith(t, s, "no_cost", `UPDATE OF cost_control_mode ON tasks`)
	if _, err := s.Duplicate(ctx, tv.Task.ID, "copy", "user"); err == nil {
		t.Fatal("duplicate succeeded with the cost policy copy failing")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM tasks`); n != 1 {
		t.Fatalf("tasks after failed duplicate: %d", n)
	}
	drop()
	cp, err := s.Duplicate(ctx, tv.Task.ID, "copy", "user")
	if err != nil || cp.CurrentVersion == nil {
		t.Fatalf("%+v %v", cp, err)
	}
}

// Concurrent commits of one entity get distinct version numbers (the read of the
// next number and the insert are one write transaction).
func TestConcurrentCommitsNumberDistinct(t *testing.T) {
	s, ctx := atomicFixture(t)
	tv, err := s.Create(ctx, CreateInput{Name: "n", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			_, err := s.Ver.Commit(ctx, version.CommitInput{EntityType: version.EntityTask, EntityID: tv.Task.ID,
				Content: map[string]int{"i": i}, NoMove: true})
			errs <- err
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, s, `SELECT COUNT(DISTINCT version_no) FROM versions WHERE entity_id=?`, tv.Task.ID); n != 9 {
		t.Fatalf("distinct version numbers: %d", n)
	}
}
