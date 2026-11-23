package tasksvc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// A workflow task runs in the workflow's folder, so it keeps no folder of its
// own: not when created, not when a prompt task becomes one, not on rollback to
// a version saved with one. Going back to a prompt version restores its folder.
func TestWorkflowTaskKeepsNoWorkspace(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ver := version.New(sqlDB)
	svc := New(store, ver, nil)
	ctx := context.Background()
	row := func(id string) string {
		var ws string
		if err := sqlDB.QueryRow(`SELECT workspace_dir FROM tasks WHERE id=?`, id).Scan(&ws); err != nil {
			t.Fatal(err)
		}
		return ws
	}

	wf, err := svc.Create(ctx, CreateInput{Name: "wf", WorkflowID: "wf_1", WorkspaceDir: "/srv/old"})
	if err != nil {
		t.Fatal(err)
	}
	if wf.Spec.WorkspaceDir != "" || row(wf.Task.ID) != "" {
		t.Fatalf("created workflow task kept a folder: %q %q", wf.Spec.WorkspaceDir, row(wf.Task.ID))
	}

	tv, err := svc.Create(ctx, CreateInput{Name: "p", Prompt: "do it", WorkspaceDir: "/srv/proj"})
	if err != nil || row(tv.Task.ID) != "/srv/proj" {
		t.Fatalf("prompt task folder: %v %q", err, row(tv.Task.ID))
	}
	spec := *tv.Spec
	spec.WorkflowID, spec.WorkflowVersion = "wf_1", 1
	got, err := svc.UpdateSpec(ctx, tv.Task.ID, spec, "user", "to workflow")
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := svc.Get(ctx, tv.Task.ID)
	if got.Spec.WorkspaceDir != "" || cur.Spec.WorkspaceDir != "" || row(tv.Task.ID) != "" {
		t.Fatalf("converted task kept its folder: %q %q %q", got.Spec.WorkspaceDir, cur.Spec.WorkspaceDir, row(tv.Task.ID))
	}

	if err := svc.Rollback(ctx, tv.Task.ID, tv.CurrentVersion.ID, "user", "back"); err != nil {
		t.Fatal(err)
	}
	if row(tv.Task.ID) != "/srv/proj" {
		t.Fatalf("rollback to the prompt version lost its folder: %q", row(tv.Task.ID))
	}

	// a version saved before this change, a workflow task with a folder
	old, err := ver.Commit(ctx, version.CommitInput{EntityType: version.EntityTask, EntityID: tv.Task.ID,
		Content: Spec{Prompt: "in", WorkflowID: "wf_1", WorkspaceDir: "/srv/stale"}, Source: "user_edit", Actor: "user", Reason: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Rollback(ctx, tv.Task.ID, tv.CurrentVersion.ID, "user", "back"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rollback(ctx, tv.Task.ID, old.Version.ID, "user", "old"); err != nil {
		t.Fatal(err)
	}
	if row(tv.Task.ID) != "" {
		t.Fatalf("rollback to an old workflow version restored a folder: %q", row(tv.Task.ID))
	}
}
