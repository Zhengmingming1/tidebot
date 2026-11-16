package gitsnap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckWritesNothing: Check answers like Apply would (fits / conflict with git's
// reason) in both directions, and leaves the files and .git as they were.
func TestCheckWritesNothing(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	c := snapRun(t, repo, func() {
		write(t, filepath.Join(repo, "a.txt"), "ONE\ntwo\nthree\n")
		write(t, filepath.Join(repo, "笔记.md"), "你好\n")
	})
	fp := gitFingerprint(t, repo)
	if err := Check(context.Background(), repo, c.Patch, true); err != nil {
		t.Fatalf("undo should fit: %v", err)
	}
	if err := Check(context.Background(), repo, c.Patch, false); err == nil {
		t.Fatal("applying again over the run's result should not fit")
	}
	write(t, filepath.Join(repo, "笔记.md"), "edited by hand\n")
	now := readAll(t, repo)
	err := Check(context.Background(), repo, c.Patch, true)
	var ce *ErrConflict
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "笔记.md") {
		t.Fatalf("want conflict naming 笔记.md, got %v", err)
	}
	if err := Check(context.Background(), repo, FilePatch(c.Patch, []string{"a.txt"}), true); err != nil {
		t.Fatalf("the other file alone should fit: %v", err)
	}
	if !same(readAll(t, repo), now) || gitFingerprint(t, repo) != fp {
		t.Fatal("Check wrote to the folder")
	}
}

// TestAlreadyApplied: after the run's patch is taken back (or put back), the
// opposite git apply --check fits, so the tree already matches that result;
// a later hand edit fits neither direction. Added, deleted and edited files
// are covered; nothing is written.
func TestAlreadyApplied(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	c := snapRun(t, repo, func() {
		write(t, filepath.Join(repo, "a.txt"), "ONE\ntwo\nthree\n")
		write(t, filepath.Join(repo, "新建", "笔记.md"), "你好\n")
		_ = os.Remove(filepath.Join(repo, "gone.txt"))
	})
	ctx := context.Background()
	already := func(reverse bool) bool {
		t.Helper()
		ok, err := AlreadyApplied(ctx, repo, c.Patch, reverse)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if already(true) {
		t.Fatal("just after the run, undo is not already done")
	}
	if !already(false) {
		t.Fatal("just after the run, the tree already matches a redo")
	}
	if err := Apply(ctx, repo, c.Patch, true); err != nil {
		t.Fatal(err)
	}
	if !already(true) {
		t.Fatal("after undo, the tree should already match a second undo")
	}
	if already(false) {
		t.Fatal("after undo, redo is not already done")
	}
	if _, err := os.Stat(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal("deleted file was not restored")
	}
	if err := Apply(ctx, repo, c.Patch, false); err != nil {
		t.Fatal(err)
	}
	if !already(false) {
		t.Fatal("after redo, the tree should already match a second redo")
	}
	if already(true) {
		t.Fatal("after redo, undo is not already done")
	}
	write(t, filepath.Join(repo, "a.txt"), "edited by hand\n")
	now := readAll(t, repo)
	fp := gitFingerprint(t, repo)
	if already(true) || already(false) {
		t.Fatal("a hand edit must not look like undo or redo already done")
	}
	if !same(readAll(t, repo), now) || gitFingerprint(t, repo) != fp {
		t.Fatal("AlreadyApplied wrote to the folder")
	}
	// a one-file section already taken back, the others still as the run left them
	write(t, filepath.Join(repo, "a.txt"), "ONE\ntwo\nthree\n")
	part := FilePatch(c.Patch, []string{"新建/笔记.md"})
	if err := Apply(ctx, repo, part, true); err != nil {
		t.Fatal(err)
	}
	ok, err := AlreadyApplied(ctx, repo, part, true)
	if err != nil || !ok {
		t.Fatalf("one-file undo already done: %v %v", ok, err)
	}
	ok, err = AlreadyApplied(ctx, repo, FilePatch(c.Patch, []string{"a.txt"}), true)
	if err != nil || ok {
		t.Fatalf("the other file is not undone: %v %v", ok, err)
	}
}
