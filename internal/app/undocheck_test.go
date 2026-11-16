package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
)

// TestCheckRunUndo: before any click the check says which undo buttons would work —
// all of them on a clean folder; a file edited after the run turns off the main
// button (naming it) and that file's own button, the others stay on; after one file is
// taken back its own button checks "put back"; a wholly undone run checks "put back
// all". The check never changes the folder.
func TestCheckRunUndo(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
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
VALUES('r1','succeeded','quick','p',?,'edit',?,?,datetime('now'))`, ws, string(fc), c.Patch); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(ws, n)); return string(b) }

	ck, err := a.checkRunUndo(ctx, "r1")
	if err != nil || ck.Code != "" || ck.Whole != "ok" || len(ck.Files) != 3 {
		t.Fatalf("clean: %+v %v", ck, err)
	}
	for p, f := range ck.Files {
		if !f.OK {
			t.Fatalf("clean file %s: %+v", p, f)
		}
	}
	// edited by hand after the run
	_ = os.WriteFile(filepath.Join(ws, "笔记.md"), []byte("改过了\n"), 0o644)
	ck, err = a.checkRunUndo(ctx, "r1")
	if err != nil || ck.Whole != "conflict" || len(ck.WholeFiles) != 1 || ck.WholeFiles[0] != "笔记.md" {
		t.Fatalf("edited: %+v %v", ck, err)
	}
	if f := ck.Files["笔记.md"]; f.OK || f.Detail == "" {
		t.Fatalf("edited file: %+v", f)
	}
	if !ck.Files["main.go"].OK || !ck.Files["new.txt"].OK {
		t.Fatalf("other files: %+v", ck.Files)
	}
	if read("笔记.md") != "改过了\n" || read("new.txt") != "new\n" || read("main.go") != "package main\n\nfunc main() {}\n" {
		t.Fatal("check changed the folder")
	}
	// one file taken back: its own button now puts it back
	if _, err := a.undoRunChanges(ctx, "r1", false, "new.txt"); err != nil {
		t.Fatal(err)
	}
	ck, _ = a.checkRunUndo(ctx, "r1")
	if !ck.Files["new.txt"].OK || ck.Whole != "conflict" {
		t.Fatalf("partly: %+v", ck)
	}
	_ = os.WriteFile(filepath.Join(ws, "new.txt"), []byte("someone else's\n"), 0o644) // put back would clash
	if ck, _ = a.checkRunUndo(ctx, "r1"); ck.Files["new.txt"].OK {
		t.Fatalf("put back over a new file: %+v", ck.Files["new.txt"])
	}
	_ = os.Remove(filepath.Join(ws, "new.txt"))
	// hand edit reverted → the rest can go; wholly undone → "put back all" checks forward
	_ = os.WriteFile(filepath.Join(ws, "笔记.md"), []byte("你好\n"), 0o644)
	if _, err := a.undoRunChanges(ctx, "r1", false, ""); err != nil {
		t.Fatal(err)
	}
	if ck, _ = a.checkRunUndo(ctx, "r1"); ck.Whole != "ok" || len(ck.Files) != 3 {
		t.Fatalf("whole undone: %+v", ck)
	}
	_ = os.WriteFile(filepath.Join(ws, "main.go"), []byte("package other\n"), 0o644)
	if ck, _ = a.checkRunUndo(ctx, "r1"); ck.Whole != "conflict" || len(ck.WholeFiles) != 1 || ck.WholeFiles[0] != "main.go" || !ck.Files["new.txt"].OK {
		t.Fatalf("put back all over an edit: %+v", ck)
	}
	// folder held by an edit run
	real, _ := filepath.EvalSymlinks(ws)
	if !lockEditDir(real) {
		t.Fatal("lock")
	}
	ck, _ = a.checkRunUndo(ctx, "r1")
	unlockEditDir(real)
	if ck.Code != "busy" {
		t.Fatalf("busy: %+v", ck)
	}
	// not offered at all: running, nothing recorded, unknown run
	_, _ = a.DB.Exec(`UPDATE runs SET status='running' WHERE id='r1'`)
	if ck, err = a.checkRunUndo(ctx, "r1"); err != nil || ck.Code != "running" {
		t.Fatalf("running: %+v %v", ck, err)
	}
	_, _ = a.DB.Exec(`UPDATE runs SET status='succeeded', run_mode='read' WHERE id='r1'`)
	if ck, _ = a.checkRunUndo(ctx, "r1"); ck.Code != "none" {
		t.Fatalf("read-only: %+v", ck)
	}
	if _, err = a.checkRunUndo(ctx, "nope"); err == nil {
		t.Fatal("unknown run")
	}
	// over the list cap: no per-file answers
	_, _ = a.DB.Exec(`UPDATE runs SET run_mode='edit', changes_undone_at='', changes_undone_files='', file_changes=json_set(file_changes, '$.count', 400) WHERE id='r1'`)
	if ck, _ = a.checkRunUndo(ctx, "r1"); ck.Code != "" || len(ck.Files) != 0 || ck.Whole == "" {
		t.Fatalf("over cap: %+v", ck)
	}
}

// When the folder is already in the post-undo (or post-redo) state but the
// run row was not marked, the pre-click check must say the button would work
// (so it is not turned off). A genuine later edit still comes back as conflict.
func TestCheckRunUndoAlreadyAppliedIsOK(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
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
VALUES('r1','succeeded','quick','p',?,'edit',?,?,datetime('now'))`, ws, string(fc), c.Patch); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// take everything back, then drop the mark: the main button and each
	// file's button must stay on so the user can finish writing the row.
	if err := gitsnap.Apply(ctx, ws, c.Patch, true); err != nil {
		t.Fatal(err)
	}
	ck, err := a.checkRunUndo(ctx, "r1")
	if err != nil || ck.Code != "" || ck.Whole != "ok" {
		t.Fatalf("already undone, row unmarked: %+v %v", ck, err)
	}
	for p, f := range ck.Files {
		if !f.OK {
			t.Fatalf("already undone file %s: %+v", p, f)
		}
	}

	_ = os.WriteFile(filepath.Join(ws, "main.go"), []byte("package other\n"), 0o644)
	ck, err = a.checkRunUndo(ctx, "r1")
	if err != nil || ck.Whole != "conflict" || ck.Files["main.go"].OK {
		t.Fatalf("hand edit: %+v %v", ck, err)
	}
	if !ck.Files["new.txt"].OK || !ck.Files["笔记.md"].OK {
		t.Fatalf("other files after a hand edit: %+v", ck.Files)
	}
	_ = os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n"), 0o644)

	// one file already taken back, the others still as the run left them,
	// row unmarked: that file's undo button stays on.
	if err := gitsnap.Apply(ctx, ws, c.Patch, false); err != nil {
		t.Fatal(err)
	}
	if err := gitsnap.Apply(ctx, ws, gitsnap.FilePatch(c.Patch, []string{"new.txt"}), true); err != nil {
		t.Fatal(err)
	}
	ck, err = a.checkRunUndo(ctx, "r1")
	if err != nil || !ck.Files["new.txt"].OK || !ck.Files["main.go"].OK || !ck.Files["笔记.md"].OK {
		t.Fatalf("one file already undone: %+v %v", ck, err)
	}

	// wholly undone and marked, then the files put back on disk without
	// clearing the row: the main button's action is redo and stays on.
	if err := gitsnap.Apply(ctx, ws, gitsnap.FilePatch(c.Patch, []string{"new.txt"}), false); err != nil {
		t.Fatal(err)
	}
	if err := gitsnap.Apply(ctx, ws, c.Patch, true); err != nil {
		t.Fatal(err)
	}
	_, _ = a.DB.Exec(`UPDATE runs SET changes_undone_at=? WHERE id='r1'`, "2026-11-16T00:00:00Z")
	if err := gitsnap.Apply(ctx, ws, c.Patch, false); err != nil {
		t.Fatal(err)
	}
	ck, err = a.checkRunUndo(ctx, "r1")
	if err != nil || ck.Whole != "ok" {
		t.Fatalf("already redone, row still marked: %+v %v", ck, err)
	}
	for p, f := range ck.Files {
		if !f.OK {
			t.Fatalf("already redone file %s: %+v", p, f)
		}
	}
}
