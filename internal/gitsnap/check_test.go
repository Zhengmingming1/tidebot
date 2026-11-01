package gitsnap

import (
	"context"
	"errors"
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
