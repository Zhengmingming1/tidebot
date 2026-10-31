package gitsnap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAll(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		m[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return m
}

func same(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// snapRun takes a snapshot, lets edit change the folder, returns the changes.
func snapRun(t *testing.T, dir string, edit func()) *Changes {
	t.Helper()
	s, err := Start(context.Background(), dir, filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	edit()
	c, err := s.Finish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestApplyUndoRedo: the run's patch taken back restores the folder byte for byte
// (edited, added, deleted, binary, CJK path; earlier uncommitted edits kept), applied
// again brings the run's result back, and .git is never written.
func TestApplyUndoRedo(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	write(t, filepath.Join(repo, "a.txt"), "one\ntwo\nthree\nmine\n") // the owner's, before the run
	before := readAll(t, repo)
	c := snapRun(t, repo, func() {
		write(t, filepath.Join(repo, "a.txt"), "ONE\ntwo\nthree\nmine\n")
		write(t, filepath.Join(repo, "新建", "笔记.md"), "你好\n")
		_ = os.Remove(filepath.Join(repo, "gone.txt"))
		if err := os.WriteFile(filepath.Join(repo, "img.bin"), []byte{0, 1, 2, 0xff, 0, 9}, 0o644); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(repo, "x.log"), "ignored\n")
	})
	if c.Count != 4 || !strings.Contains(c.Patch, "GIT binary patch") {
		t.Fatalf("changes %+v\n%s", c, c.Patch)
	}
	after := readAll(t, repo)
	fp := gitFingerprint(t, repo)
	if err := Apply(context.Background(), repo, c.Patch, true); err != nil {
		t.Fatal(err)
	}
	got := readAll(t, repo)
	before["x.log"] = "ignored\n" // ignored files aren't the run's to take back
	if !same(got, before) {
		t.Fatalf("undo left %v, want %v", got, before)
	}
	if _, err := os.Stat(filepath.Join(repo, "新建")); !os.IsNotExist(err) {
		t.Fatalf("emptied folder kept: %v", err)
	}
	if err := Apply(context.Background(), repo, c.Patch, false); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, repo); !same(got, after) {
		t.Fatalf("redo left %v, want %v", got, after)
	}
	if gitFingerprint(t, repo) != fp {
		t.Fatal(".git changed")
	}
}

// TestApplyConflictTouchesNothing: a file edited after the run makes the whole undo
// refuse, with git's reason, and no other file is changed either.
func TestApplyConflictTouchesNothing(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	c := snapRun(t, repo, func() {
		write(t, filepath.Join(repo, "a.txt"), "ONE\ntwo\nthree\n")
		write(t, filepath.Join(repo, "sub", "s.txt"), "S\n")
	})
	write(t, filepath.Join(repo, "sub", "s.txt"), "S edited by hand\n")
	now := readAll(t, repo)
	err := Apply(context.Background(), repo, c.Patch, true)
	var ce *ErrConflict
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "s.txt") {
		t.Fatalf("want conflict naming s.txt, got %v", err)
	}
	if !same(readAll(t, repo), now) {
		t.Fatal("a refused undo changed files")
	}
}

// TestApplySubfolder: a run in a subfolder of a bigger repo takes back only its own
// paths, which are relative to that folder.
func TestApplySubfolder(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	sub := filepath.Join(repo, "sub")
	c := snapRun(t, sub, func() {
		write(t, filepath.Join(sub, "s.txt"), "s2\n")
		write(t, filepath.Join(sub, "n.txt"), "n\n")
	})
	if c.Count != 2 || strings.Contains(c.Patch, "sub/") {
		t.Fatalf("changes %+v %s", c, c.Patch)
	}
	if err := Apply(context.Background(), sub, c.Patch, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(sub, "s.txt")); string(b) != "s\n" {
		t.Fatalf("s.txt %q", b)
	}
	if _, err := os.Stat(filepath.Join(sub, "n.txt")); !os.IsNotExist(err) {
		t.Fatal("n.txt kept")
	}
}

// TestApplyRefusesEscapingPaths: a patch naming a path outside the folder is refused.
func TestApplyRefusesEscapingPaths(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	outside := filepath.Join(filepath.Dir(repo), "evil.txt")
	p := "diff --git a/../evil.txt b/../evil.txt\nnew file mode 100644\n--- /dev/null\n+++ b/../evil.txt\n@@ -0,0 +1 @@\n+x\n"
	if err := Apply(context.Background(), repo, p, false); err == nil {
		t.Fatal("escaping path applied")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("file written outside the folder")
	}
}
