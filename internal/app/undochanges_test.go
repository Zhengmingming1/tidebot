package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
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

func clearUndoMark(t *testing.T, a *App, runID string) {
	t.Helper()
	if _, err := a.DB.Exec(`UPDATE runs SET changes_undone_at='', changes_undone_files='' WHERE id=?`, runID); err != nil {
		t.Fatal(err)
	}
}

func setUndoMark(t *testing.T, a *App, runID, at, files string) {
	t.Helper()
	if _, err := a.DB.Exec(`UPDATE runs SET changes_undone_at=?, changes_undone_files=? WHERE id=?`, at, files, runID); err != nil {
		t.Fatal(err)
	}
}

func insertThreeFileRun(t *testing.T, a *App, id string) string {
	t.Helper()
	ws := gitRepo(t)
	s, err := gitsnap.Start(context.Background(), ws, filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(ws, "new.txt"), []byte("new\n"), 0o644)
	_ = os.WriteFile(filepath.Join(ws, "笔记.md"), []byte("你好\n"), 0o644)
	c, err := s.Finish(context.Background())
	s.Close()
	if err != nil || c.Count != 3 {
		t.Fatalf("%+v %v", c, err)
	}
	fc, _ := json.Marshal(c)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, trigger_kind, prompt, workspace_dir, run_mode, file_changes, file_patch, created_at)
VALUES(?,'succeeded','quick','p',?,'edit',?,?,datetime('now'))`, id, ws, string(fc), c.Patch); err != nil {
		t.Fatal(err)
	}
	return ws
}

// Folder already in the post-undo (or post-redo) state with the run row still
// unmarked: a second click only writes the mark and leaves the files alone.
// A genuine later edit still returns conflict and does not mark. A mark that
// gave up, then a second call, ends with the row marked. Same for one file.
// The "already undone" and "failed mark then second click" cases failed on
// the pre-change code (git apply reported a conflict).
func TestUndoRunChangesMarksWhenFolderAlreadyMatches(t *testing.T) {
	ctx := context.Background()

	t.Run("already undone", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		runID, made, _ := startUndoableRun(t, a)
		res, err := a.undoRunChanges(ctx, runID, false, "")
		if err != nil || res.UndoneAt == "" {
			t.Fatalf("undo: %+v %v", res, err)
		}
		clearUndoMark(t, a, runID)
		res2, err := a.undoRunChanges(ctx, runID, false, "")
		if err != nil || res2.UndoneAt == "" {
			t.Fatalf("second undo with files already taken back: %+v %v", res2, err)
		}
		if _, st := os.Stat(made); !os.IsNotExist(st) {
			t.Fatal("second undo recreated the file")
		}
		if saved := undoneAtOf(t, a, runID); saved != res2.UndoneAt {
			t.Fatalf("saved %q want %q", saved, res2.UndoneAt)
		}
	})

	t.Run("hand edit still conflicts", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		runID, made, _ := startUndoableRun(t, a)
		if err := os.WriteFile(made, []byte("edited by hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := a.undoRunChanges(ctx, runID, false, ""); undoCode(err) != "conflict" {
			t.Fatalf("hand edit: %v", err)
		}
		if b, _ := os.ReadFile(made); string(b) != "edited by hand\n" {
			t.Fatalf("refused undo changed the file: %q", b)
		}
		if at := undoneAtOf(t, a, runID); at != "" {
			t.Fatalf("row marked after conflict: %q", at)
		}
	})

	t.Run("already redone", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		runID, made, _ := startUndoableRun(t, a)
		res, err := a.undoRunChanges(ctx, runID, false, "")
		if err != nil || res.UndoneAt == "" {
			t.Fatalf("undo: %+v %v", res, err)
		}
		if _, err := a.undoRunChanges(ctx, runID, true, ""); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(made)
		if err != nil || len(before) == 0 {
			t.Fatalf("redo did not bring the file back: %v", err)
		}
		setUndoMark(t, a, runID, res.UndoneAt, "")
		res2, err := a.undoRunChanges(ctx, runID, true, "")
		if err != nil || res2.UndoneAt != "" {
			t.Fatalf("second redo with files already put back: %+v %v", res2, err)
		}
		if b, _ := os.ReadFile(made); string(b) != string(before) {
			t.Fatalf("second redo changed the file: %q", b)
		}
		if at := undoneAtOf(t, a, runID); at != "" {
			t.Fatalf("row still marked after redo: %q", at)
		}
	})

	t.Run("redo hand edit still conflicts", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		runID, made, _ := startUndoableRun(t, a)
		if _, err := a.undoRunChanges(ctx, runID, false, ""); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(made, []byte("edited by hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := a.undoRunChanges(ctx, runID, true, ""); undoCode(err) != "conflict" {
			t.Fatalf("redo over a hand edit: %v", err)
		}
		if b, _ := os.ReadFile(made); string(b) != "edited by hand\n" {
			t.Fatalf("refused redo changed the file: %q", b)
		}
		if at := undoneAtOf(t, a, runID); at == "" {
			t.Fatal("conflict cleared the undone mark")
		}
	})

	t.Run("failed mark then second click", func(t *testing.T) {
		wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
		t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
		writeRetryWait, writeRetryPause, writeRetryFor = 20*time.Millisecond, time.Millisecond, 40*time.Millisecond

		a := newTestApp(t)
		fakeHome(t)
		runID, made, _ := startUndoableRun(t, a)
		blockSQL(t, a, "undoretry", `BEFORE UPDATE ON runs WHEN NEW.changes_undone_at<>''`)
		if _, err := a.undoRunChanges(ctx, runID, false, ""); err == nil {
			t.Fatal("expected error after giving up")
		}
		if _, st := os.Stat(made); !os.IsNotExist(st) {
			t.Fatal("folder was not taken back")
		}
		if at := undoneAtOf(t, a, runID); at != "" {
			t.Fatalf("row marked after giving up: %q", at)
		}
		unblockSQL(t, a, "undoretry")
		res, err := a.undoRunChanges(ctx, runID, false, "")
		if err != nil || res.UndoneAt == "" {
			t.Fatalf("second click: %+v %v", res, err)
		}
		if _, st := os.Stat(made); !os.IsNotExist(st) {
			t.Fatal("second click changed the folder")
		}
		if saved := undoneAtOf(t, a, runID); saved != res.UndoneAt {
			t.Fatalf("saved %q want %q", saved, res.UndoneAt)
		}
	})

	t.Run("one file already undone", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		ws := insertThreeFileRun(t, a, "r-match")
		if _, err := a.undoRunChanges(ctx, "r-match", false, "new.txt"); err != nil {
			t.Fatal(err)
		}
		clearUndoMark(t, a, "r-match")
		mainBefore, _ := os.ReadFile(filepath.Join(ws, "main.go"))
		res, err := a.undoRunChanges(ctx, "r-match", false, "new.txt")
		if err != nil || res.UndoneAt != "" || len(res.UndoneFiles) != 1 || res.UndoneFiles[0] != "new.txt" {
			t.Fatalf("second one-file undo: %+v %v", res, err)
		}
		if _, st := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(st) {
			t.Fatal("second one-file undo recreated the file")
		}
		if b, _ := os.ReadFile(filepath.Join(ws, "main.go")); string(b) != string(mainBefore) {
			t.Fatal("second one-file undo changed another file")
		}
		var files string
		_ = a.DB.QueryRow(`SELECT changes_undone_files FROM runs WHERE id='r-match'`).Scan(&files)
		if files != `["new.txt"]` {
			t.Fatalf("saved files %q", files)
		}
	})

	t.Run("one file already redone", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		ws := insertThreeFileRun(t, a, "r-match")
		if _, err := a.undoRunChanges(ctx, "r-match", false, "new.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.undoRunChanges(ctx, "r-match", true, "new.txt"); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(filepath.Join(ws, "new.txt"))
		if err != nil || string(before) != "new\n" {
			t.Fatalf("put back: %q %v", before, err)
		}
		setUndoMark(t, a, "r-match", "", `["new.txt"]`)
		res, err := a.undoRunChanges(ctx, "r-match", true, "new.txt")
		if err != nil || len(res.UndoneFiles) != 0 {
			t.Fatalf("second one-file redo: %+v %v", res, err)
		}
		if b, _ := os.ReadFile(filepath.Join(ws, "new.txt")); string(b) != string(before) {
			t.Fatalf("second one-file redo changed the file: %q", b)
		}
		var at, files string
		_ = a.DB.QueryRow(`SELECT changes_undone_at, changes_undone_files FROM runs WHERE id='r-match'`).Scan(&at, &files)
		if at != "" || files != "" {
			t.Fatalf("row still marked: %q %q", at, files)
		}
	})

	t.Run("one file hand edit still conflicts", func(t *testing.T) {
		a := newTestApp(t)
		fakeHome(t)
		ws := insertThreeFileRun(t, a, "r-match")
		p := filepath.Join(ws, "new.txt")
		if err := os.WriteFile(p, []byte("edited by hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := a.undoRunChanges(ctx, "r-match", false, "new.txt"); undoCode(err) != "conflict" {
			t.Fatalf("one-file hand edit: %v", err)
		}
		if b, _ := os.ReadFile(p); string(b) != "edited by hand\n" {
			t.Fatalf("refused one-file undo changed the file: %q", b)
		}
		var files string
		_ = a.DB.QueryRow(`SELECT changes_undone_files FROM runs WHERE id='r-match'`).Scan(&files)
		if files != "" {
			t.Fatalf("row marked after conflict: %q", files)
		}
	})
}
