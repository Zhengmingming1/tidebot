package version

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestCommitRollbackTagAndOpLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx := context.Background()

	r1, err := s.Commit(ctx, CommitInput{
		EntityType: EntityTask, EntityID: "task_a",
		Content: map[string]any{"prompt": "v1"}, Source: "user_edit", Actor: "user", Reason: "create",
	})
	if err != nil || !r1.Created {
		t.Fatalf("%+v %v", r1, err)
	}
	r2, err := s.Commit(ctx, CommitInput{
		EntityType: EntityTask, EntityID: "task_a",
		Content: map[string]any{"prompt": "v2"}, Source: "user_edit", Actor: "user", Reason: "edit",
	})
	if err != nil || !r2.Created {
		t.Fatal(err)
	}
	if r2.Version.ParentVersionID.String != r1.Version.ID {
		t.Fatalf("parent=%v want %s", r2.Version.ParentVersionID, r1.Version.ID)
	}
	// no-op same content
	rNop, err := s.Commit(ctx, CommitInput{
		EntityType: EntityTask, EntityID: "task_a",
		Content: map[string]any{"prompt": "v2"}, Actor: "user",
	})
	if err != nil || !rNop.SameAsCurrent || rNop.Created {
		t.Fatalf("%+v", rNop)
	}
	// rollback pointer to v1
	if err := s.MovePointer(ctx, EntityTask, "task_a", PointerCurrent, r1.Version.ID, "user", "rollback", ""); err != nil {
		t.Fatal(err)
	}
	cur, err := s.CurrentVersion(ctx, EntityTask, "task_a")
	if err != nil || cur.ID != r1.Version.ID {
		t.Fatalf("%+v %v", cur, err)
	}
	// edit after rollback forks from v1
	r3, err := s.Commit(ctx, CommitInput{
		EntityType: EntityTask, EntityID: "task_a",
		Content: map[string]any{"prompt": "v3-from-v1"}, Actor: "user", Reason: "fork",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r3.Version.ParentVersionID.String != r1.Version.ID {
		t.Fatalf("fork parent=%v", r3.Version.ParentVersionID)
	}
	if r3.Version.VersionNo != 3 {
		t.Fatalf("version_no=%d", r3.Version.VersionNo)
	}
	// reuse hash: change back to v1 content moves pointer without new version
	rReuse, err := s.Commit(ctx, CommitInput{
		EntityType: EntityTask, EntityID: "task_a",
		Content: map[string]any{"prompt": "v1"}, Actor: "user", Reason: "back to v1 content",
	})
	if err != nil || rReuse.Created || !rReuse.PointerMoved {
		t.Fatalf("%+v %v", rReuse, err)
	}
	if rReuse.Version.ID != r1.Version.ID {
		t.Fatalf("reuse id=%s", rReuse.Version.ID)
	}
	if err := s.SetTag(ctx, EntityTask, "task_a", "stable", r1.Version.ID, "user", "tag"); err != nil {
		t.Fatal(err)
	}
	if err := s.AssertOpLogAppendOnly(ctx); err != nil {
		t.Fatal(err)
	}
	logs, err := s.ListOpLog(ctx, EntityTask, "task_a", 20)
	if err != nil || len(logs) < 3 {
		t.Fatalf("logs=%d %v", len(logs), err)
	}
}

func TestHashStable(t *testing.T) {
	h1, _, err := HashJSON(map[string]any{"a": 1, "b": "x"})
	if err != nil {
		t.Fatal(err)
	}
	h2, _, err := HashJSON(map[string]any{"a": 1, "b": "x"})
	if err != nil || h1 != h2 {
		t.Fatalf("%s vs %s", h1, h2)
	}
}
