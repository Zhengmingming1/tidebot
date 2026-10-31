package gitsnap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fingerprint of everything under .git: path, size and content hash.
func gitFingerprint(t *testing.T, repo string) string {
	t.Helper()
	var rows []string
	root := filepath.Join(repo, ".git")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		rows = append(rows, fmt.Sprintf("%s %x", filepath.ToSlash(rel), sha256.Sum256(b)))
		return nil
	})
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	run(t, repo, "config", "core.autocrlf", "false")
	write(t, filepath.Join(repo, "a.txt"), "one\ntwo\nthree\n")
	write(t, filepath.Join(repo, "gone.txt"), "bye\n")
	write(t, filepath.Join(repo, "sub", "s.txt"), "s\n")
	write(t, filepath.Join(repo, ".gitignore"), "*.log\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-qm", "init")
	return repo
}

func TestChangesWithoutTouchingRepo(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	// uncommitted before the run: must not be reported as the run's change
	write(t, filepath.Join(repo, "a.txt"), "one\ntwo\nthree\nmine\n")
	write(t, filepath.Join(repo, "draft.md"), "draft\n")
	before := gitFingerprint(t, repo)
	scratch := filepath.Join(t.TempDir(), "snap")
	ctx := context.Background()
	s, err := Start(ctx, repo, scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// the run
	write(t, filepath.Join(repo, "a.txt"), "one\nTWO\nthree\nmine\n")
	write(t, filepath.Join(repo, "new 文件.txt"), "x\ny\n")
	write(t, filepath.Join(repo, "debug.log"), "ignored\n")
	write(t, filepath.Join(repo, "bin.dat"), "\x00\x01\x02")
	if err := os.Remove(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	c, err := s.Finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]File{}
	for _, f := range c.Files {
		got[f.Path] = f
	}
	if len(got) != 4 || c.Count != 4 {
		t.Fatalf("files = %+v", c.Files)
	}
	if f := got["a.txt"]; f.Status != "M" || f.Add != 1 || f.Del != 1 {
		t.Errorf("a.txt = %+v", f)
	}
	if f := got["new 文件.txt"]; f.Status != "A" || f.Add != 2 {
		t.Errorf("new file = %+v", f)
	}
	if f := got["gone.txt"]; f.Status != "D" || f.Del != 1 {
		t.Errorf("gone = %+v", f)
	}
	if f := got["bin.dat"]; !f.Binary {
		t.Errorf("bin = %+v", f)
	}
	if _, ok := got["draft.md"]; ok {
		t.Error("a change made before the run is reported")
	}
	if !strings.Contains(c.Patch, "-two\n+TWO\n") || strings.Contains(c.Patch, "+mine") {
		t.Errorf("patch:\n%s", c.Patch)
	}
	if c.Add != 3 || c.Del != 2 {
		t.Errorf("totals +%d -%d", c.Add, c.Del)
	}
	if after := gitFingerprint(t, repo); after != before {
		t.Errorf(".git changed:\nbefore\n%s\nafter\n%s", before, after)
	}
	s.Close()
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Error("scratch left behind")
	}
}

func TestSubfolderOnlyAndNoChange(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	ctx := context.Background()
	s, err := Start(ctx, filepath.Join(repo, "sub"), filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.Finish(ctx)
	if err != nil || c.Count != 0 || len(c.Files) != 0 || c.Patch != "" {
		t.Fatalf("no change: %+v %v", c, err)
	}
	write(t, filepath.Join(repo, "outside.txt"), "o\n")
	write(t, filepath.Join(repo, "sub", "s.txt"), "s2\n")
	c, err = s.Finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Count != 1 || c.Files[0].Path != "s.txt" {
		t.Fatalf("subfolder: %+v", c.Files)
	}
}

func TestNotRepo(t *testing.T) {
	needGit(t)
	if _, err := Start(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "s")); err != ErrNotRepo {
		t.Fatalf("err = %v", err)
	}
}

func TestPatchCapAndFileCap(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	ctx := context.Background()
	s, err := Start(ctx, repo, filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	line := strings.Repeat("x", 99) + "\n"
	for i := 0; i < MaxFiles+5; i++ {
		write(t, filepath.Join(repo, "gen", fmt.Sprintf("f%03d.txt", i)), strings.Repeat(line, 20))
	}
	c, err := s.Finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Count != MaxFiles+5 || len(c.Files) != MaxFiles {
		t.Errorf("count %d listed %d", c.Count, len(c.Files))
	}
	if !c.PatchCut || len(c.Patch) > MaxPatch || !strings.HasSuffix(c.Patch, "\n") {
		t.Errorf("patch cut=%v len=%d", c.PatchCut, len(c.Patch))
	}
}

func TestFiltersDisabled(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	// a clean filter that would fail (and could write anywhere): must not run
	run(t, repo, "config", "filter.boom.clean", "false")
	run(t, repo, "config", "filter.boom.required", "true")
	write(t, filepath.Join(repo, ".gitattributes"), "*.b filter=boom\n")
	ctx := context.Background()
	s, err := Start(ctx, repo, filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write(t, filepath.Join(repo, "x.b"), "data\n")
	c, err := s.Finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Count != 1 || c.Files[0].Path != "x.b" {
		t.Fatalf("%+v", c.Files)
	}
}

func TestParseRawNumstat(t *testing.T) {
	in := ":100644 100644 aaa bbb M\x00a b.txt\x00:000000 100644 000 ccc A\x00img.png\x001\t2\ta b.txt\x00-\t-\timg.png\x00"
	var c Changes
	parseRawNumstat([]byte(in), &c)
	if c.Count != 2 || c.Files[0].Path != "a b.txt" || c.Files[0].Add != 1 || c.Files[0].Del != 2 || !c.Files[1].Binary || c.Files[1].Status != "A" {
		t.Fatalf("%+v", c)
	}
}
