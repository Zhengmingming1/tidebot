package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
)

// One file of an edit run is undone on its own and put back; "Undo the rest" takes
// back the others; once every file is back out the run counts as undone; a single
// file can be put back from a wholly undone run.
func TestUndoRunChangesOneFile(t *testing.T) {
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
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(ws, n)); return err == nil }
	saved := func() (string, string) {
		var at, files string
		_ = a.DB.QueryRow(`SELECT changes_undone_at, changes_undone_files FROM runs WHERE id='r1'`).Scan(&at, &files)
		return at, files
	}

	if _, err := a.undoRunChanges(ctx, "r1", true, "new.txt"); undoCode(err) != "state" {
		t.Fatalf("put back a file not undone: %v", err)
	}
	if _, err := a.undoRunChanges(ctx, "r1", false, "elsewhere.txt"); undoCode(err) != "file" {
		t.Fatalf("unknown file: %v", err)
	}
	res, err := a.undoRunChanges(ctx, "r1", false, "new.txt")
	if err != nil || res.UndoneAt != "" || len(res.UndoneFiles) != 1 || res.UndoneFiles[0] != "new.txt" {
		t.Fatalf("one file: %+v %v", res, err)
	}
	if exists("new.txt") || !exists("笔记.md") {
		t.Fatal("one file undo touched the wrong files")
	}
	if at, files := saved(); at != "" || files != `["new.txt"]` {
		t.Fatalf("saved %q %q", at, files)
	}
	if _, err := a.undoRunChanges(ctx, "r1", false, "new.txt"); undoCode(err) != "state" {
		t.Fatalf("same file twice: %v", err)
	}
	if res, err = a.undoRunChanges(ctx, "r1", true, "new.txt"); err != nil || len(res.UndoneFiles) != 0 || !exists("new.txt") {
		t.Fatalf("put back one: %+v %v", res, err)
	}
	if at, files := saved(); at != "" || files != "" {
		t.Fatalf("after put back %q %q", at, files)
	}
	// one out, then the rest → wholly undone
	if _, err = a.undoRunChanges(ctx, "r1", false, "笔记.md"); err != nil {
		t.Fatal(err)
	}
	if res, err = a.undoRunChanges(ctx, "r1", false, ""); err != nil || res.UndoneAt == "" {
		t.Fatalf("undo the rest: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "main.go")); string(b) != "package main\n" || exists("new.txt") || exists("笔记.md") {
		t.Fatal("folder not back to before the run")
	}
	if at, files := saved(); at == "" || files != "" {
		t.Fatalf("whole: %q %q", at, files)
	}
	// one file back from a wholly undone run → the other two stay undone
	if res, err = a.undoRunChanges(ctx, "r1", true, "main.go"); err != nil || res.UndoneAt != "" || len(res.UndoneFiles) != 2 {
		t.Fatalf("put back one of whole: %+v %v", res, err)
	}
	// "Put back" for the run brings the two others
	if res, err = a.undoRunChanges(ctx, "r1", true, ""); err != nil || res.UndoneAt != "" || len(res.UndoneFiles) != 0 {
		t.Fatalf("put back all: %+v %v", res, err)
	}
	if !exists("new.txt") || !exists("笔记.md") {
		t.Fatal("put back missed files")
	}
	// undoing each file one by one ends wholly undone too
	for _, f := range []string{"main.go", "new.txt", "笔记.md"} {
		if res, err = a.undoRunChanges(ctx, "r1", false, f); err != nil {
			t.Fatal(f, err)
		}
	}
	if res.UndoneAt == "" || len(res.UndoneFiles) != 0 {
		t.Fatalf("all one by one: %+v", res)
	}
	if _, err := a.undoRunChanges(ctx, "r1", false, ""); undoCode(err) != "state" {
		t.Fatalf("undo after all: %v", err)
	}
	// more files changed than listed: only the whole run
	_, _ = a.DB.Exec(`UPDATE runs SET changes_undone_at='', file_changes=json_set(file_changes,'$.count',500) WHERE id='r1'`)
	if _, err := a.undoRunChanges(ctx, "r1", false, "new.txt"); undoCode(err) != "many" {
		t.Fatalf("over the list cap: %v", err)
	}
}
