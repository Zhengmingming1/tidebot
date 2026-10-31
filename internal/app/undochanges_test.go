package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func undoCode(err error) string {
	var ue *webui.UndoError
	if errors.As(err, &ue) {
		return ue.Code
	}
	return ""
}

// "Undo changes" takes the run's file back out of the folder, refuses to do it
// twice, puts it back on redo, and refuses (touching nothing) once the file was
// edited by hand; a folder held by an edit run is busy.
func TestUndoRunChanges(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ws := gitRepo(t)
	made := filepath.Join(ws, "made-by-run.txt")
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", made)
	runID, err := a.startRun(webui.RunRequest{Prompt: "fix it", Workspace: ws, Mode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	real, _ := filepath.EvalSymlinks(ws)
	waitFor(t, 5*time.Second, func() bool {
		if lockEditDir(real) {
			unlockEditDir(real)
			return true
		}
		return false
	})
	ctx := context.Background()
	if _, err := a.undoRunChanges(ctx, runID, true); undoCode(err) != "state" {
		t.Fatalf("redo before undo: %v", err)
	}
	at, err := a.undoRunChanges(ctx, runID, false)
	if err != nil || at == "" {
		t.Fatalf("undo: %q %v", at, err)
	}
	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Fatal("file the run made is still there")
	}
	var saved string
	_ = a.DB.QueryRow(`SELECT changes_undone_at FROM runs WHERE id=?`, runID).Scan(&saved)
	if saved != at {
		t.Fatalf("saved %q want %q", saved, at)
	}
	if _, err := a.undoRunChanges(ctx, runID, false); undoCode(err) != "state" {
		t.Fatalf("second undo: %v", err)
	}
	if at, err := a.undoRunChanges(ctx, runID, true); err != nil || at != "" {
		t.Fatalf("redo: %q %v", at, err)
	}
	if b, _ := os.ReadFile(made); len(b) == 0 {
		t.Fatal("redo didn't bring the file back")
	}
	_ = os.WriteFile(made, []byte("edited by hand\n"), 0o644)
	if _, err := a.undoRunChanges(ctx, runID, false); undoCode(err) != "conflict" {
		t.Fatalf("undo after a hand edit: %v", err)
	}
	if b, _ := os.ReadFile(made); string(b) != "edited by hand\n" {
		t.Fatalf("refused undo changed the file: %q", b)
	}
	if !lockEditDir(real) {
		t.Fatal("undo left the folder held")
	}
	_, err = a.undoRunChanges(ctx, runID, false)
	unlockEditDir(real)
	if undoCode(err) != "busy" {
		t.Fatalf("undo while an edit run holds the folder: %v", err)
	}
	if _, err := a.undoRunChanges(ctx, "nope", false); !errors.Is(err, webui.ErrRunNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
}

// Runs with nothing to take back, a cut patch, or an older patch without binary data
// are refused before the folder is looked at.
func TestUndoRunChangesRefusals(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ws := gitRepo(t)
	ins := func(id, mode, status, fc, patch string) {
		t.Helper()
		if _, err := a.DB.Exec(`INSERT INTO runs(id, status, trigger_kind, prompt, workspace_dir, run_mode, file_changes, file_patch, created_at)
VALUES(?,?,?,?,?,?,?,?,datetime('now'))`, id, status, "quick", "p", ws, mode, fc, patch); err != nil {
			t.Fatal(err)
		}
	}
	p := "diff --git a/x b/x\n"
	ins("r-read", "read", "succeeded", "", "")
	ins("r-zero", "edit", "succeeded", `{"files":[],"count":0}`, "")
	ins("r-live", "edit", "running", `{"count":1}`, p)
	ins("r-cut", "edit", "succeeded", `{"count":1,"patch_cut":true}`, p)
	ins("r-bin", "edit", "succeeded", `{"count":1}`, p+"Binary files a/x and b/x differ\n")
	for id, want := range map[string]string{"r-read": "none", "r-zero": "none", "r-live": "running", "r-cut": "cut", "r-bin": "binary"} {
		if _, err := a.undoRunChanges(context.Background(), id, false); undoCode(err) != want {
			t.Fatalf("%s: %v, want %s", id, err, want)
		}
	}
}
