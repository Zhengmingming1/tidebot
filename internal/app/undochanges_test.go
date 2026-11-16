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
	if _, err := a.undoRunChanges(ctx, runID, true, ""); undoCode(err) != "state" {
		t.Fatalf("redo before undo: %v", err)
	}
	res, err := a.undoRunChanges(ctx, runID, false, "")
	at := res.UndoneAt
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
	if _, err := a.undoRunChanges(ctx, runID, false, ""); undoCode(err) != "state" {
		t.Fatalf("second undo: %v", err)
	}
	if res, err := a.undoRunChanges(ctx, runID, true, ""); err != nil || res.UndoneAt != "" {
		t.Fatalf("redo: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(made); len(b) == 0 {
		t.Fatal("redo didn't bring the file back")
	}
	_ = os.WriteFile(made, []byte("edited by hand\n"), 0o644)
	if _, err := a.undoRunChanges(ctx, runID, false, ""); undoCode(err) != "conflict" {
		t.Fatalf("undo after a hand edit: %v", err)
	}
	if b, _ := os.ReadFile(made); string(b) != "edited by hand\n" {
		t.Fatalf("refused undo changed the file: %q", b)
	}
	if !lockEditDir(real) {
		t.Fatal("undo left the folder held")
	}
	_, err = a.undoRunChanges(ctx, runID, false, "")
	unlockEditDir(real)
	if undoCode(err) != "busy" {
		t.Fatalf("undo while an edit run holds the folder: %v", err)
	}
	if _, err := a.undoRunChanges(ctx, "nope", false, ""); !errors.Is(err, webui.ErrRunNotFound) {
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
		if _, err := a.undoRunChanges(context.Background(), id, false, ""); undoCode(err) != want {
			t.Fatalf("%s: %v, want %s", id, err, want)
		}
	}
}

// startUndoableRun makes an edit run that created made-by-run.txt and waits
// until the folder is free to undo.
func startUndoableRun(t *testing.T, a *App) (runID, made, real string) {
	t.Helper()
	ws := gitRepo(t)
	made = filepath.Join(ws, "made-by-run.txt")
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", made)
	var err error
	runID, err = a.startRun(webui.RunRequest{Prompt: "fix it", Workspace: ws, Mode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	real, _ = filepath.EvalSymlinks(ws)
	waitFor(t, 5*time.Second, func() bool {
		if lockEditDir(real) {
			unlockEditDir(real)
			return true
		}
		return false
	})
	return runID, made, real
}

func undoneAtOf(t *testing.T, a *App, runID string) string {
	t.Helper()
	var at string
	if err := a.DB.QueryRow(`SELECT changes_undone_at FROM runs WHERE id=?`, runID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// After the folder is already taken back, a busy database used to get one 5s
// try; failure left the run looking live, and a second click then hit a
// conflict. The mark is retried, and a canceled request does not abort it.
func TestUndoRunChangesRetriesWhenDatabaseBusy(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	fakeHome(t)
	runID, made, _ := startUndoableRun(t, a)
	blockSQL(t, a, "undo", `BEFORE UPDATE ON runs WHEN NEW.changes_undone_at<>''`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res webui.UndoResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := a.undoRunChanges(ctx, runID, false, "")
		done <- outcome{res, err}
	}()

	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(made)
		return os.IsNotExist(err)
	})
	time.Sleep(30 * time.Millisecond)
	if at := undoneAtOf(t, a, runID); at != "" {
		t.Fatalf("marked before unlock: %q", at)
	}
	cancel() // the request went away after the folder changed
	time.Sleep(20 * time.Millisecond)
	if at := undoneAtOf(t, a, runID); at != "" {
		t.Fatalf("marked after cancel, before unlock: %q", at)
	}

	unblockSQL(t, a, "undo")
	var out outcome
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not finish")
	}
	if out.err != nil || out.res.UndoneAt == "" {
		t.Fatalf("undo: %+v %v", out.res, out.err)
	}
	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Fatal("file the run made came back")
	}
	if saved := undoneAtOf(t, a, runID); saved != out.res.UndoneAt {
		t.Fatalf("saved %q want %q", saved, out.res.UndoneAt)
	}
}

func TestUndoRunChangesReturnsErrorIfMarkGivesUp(t *testing.T) {
	wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
	t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
	writeRetryWait, writeRetryPause, writeRetryFor = 20*time.Millisecond, time.Millisecond, 40*time.Millisecond

	a := newTestApp(t)
	fakeHome(t)
	runID, made, _ := startUndoableRun(t, a)
	blockSQL(t, a, "undofail", `BEFORE UPDATE ON runs WHEN NEW.changes_undone_at<>''`)

	res, err := a.undoRunChanges(context.Background(), runID, false, "")
	if err == nil {
		t.Fatal("expected error after giving up")
	}
	if _, st := os.Stat(made); !os.IsNotExist(st) {
		t.Fatal("folder was not taken back")
	}
	if res.UndoneAt == "" {
		t.Fatal("result has no undone time to retry the mark with")
	}
	if at := undoneAtOf(t, a, runID); at != "" {
		t.Fatalf("row marked after giving up: %q", at)
	}
}
