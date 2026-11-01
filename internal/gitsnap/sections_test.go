package gitsnap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSectionHeader(t *testing.T) {
	for in, want := range map[string]string{
		"a.txt":       "diff --git a/a.txt b/a.txt",
		"新建/笔记.md":    "diff --git a/新建/笔记.md b/新建/笔记.md",
		"with space":  "diff --git a/with space b/with space",
		`q"uote`:      `diff --git "a/q\"uote" "b/q\"uote"`,
		"t\tab\x01\\": `diff --git "a/t\tab\001\\" "b/t\tab\001\\"`,
	} {
		if got := sectionHeader(in); got != want {
			t.Fatalf("%q: %q want %q", in, got, want)
		}
	}
}

// A type change gives two sections for one path: both are picked; a path that is a
// prefix of another ("a" / "a b") never matches the wrong section.
func TestFilePatchPicks(t *testing.T) {
	p := "diff --git a/a b/a\ndeleted file mode 100644\n--- a/a\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n" +
		"diff --git a/a b/a\nnew file mode 120000\n--- /dev/null\n+++ b/a\n@@ -0,0 +1 @@\n+t\n" +
		"diff --git a/a b b/a b\n--- a/a b\n+++ b/a b\n@@ -1 +1 @@\n-1\n+2\n"
	got := FilePatch(p, []string{"a"})
	if strings.Count(got, "diff --git") != 2 || strings.Contains(got, "a/a b b/") {
		t.Fatalf("FilePatch a:\n%s", got)
	}
	rest := PatchWithout(p, []string{"a"})
	if !strings.HasPrefix(rest, "diff --git a/a b b/a b\n") || strings.Count(rest, "diff --git") != 1 {
		t.Fatalf("PatchWithout a:\n%s", rest)
	}
	if FilePatch(p, []string{"nope"}) != "" || PatchWithout(p, nil) != p {
		t.Fatal("no match / no exclusion")
	}
	if got := FilePatch(strings.ReplaceAll(p, "\n", "\r\n"), []string{"a b"}); strings.Count(got, "diff --git") != 1 {
		t.Fatalf("CRLF patch:\n%q", got)
	}
}

// TestApplyOneFile: one file of the run goes back on its own (other files and the
// owner's earlier edit stay), the rest goes back without it, and it comes back alone.
func TestApplyOneFile(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	write(t, filepath.Join(repo, "a.txt"), "one\ntwo\nthree\nmine\n")
	odd := "new file.txt"
	if runtime.GOOS != "windows" { // quotes aren't allowed in Windows file names
		odd = `say "hi".txt`
	}
	before := readAll(t, repo)
	c := snapRun(t, repo, func() {
		write(t, filepath.Join(repo, "a.txt"), "ONE\ntwo\nthree\nmine\n")
		write(t, filepath.Join(repo, "新建", "笔记.md"), "你好\n")
		write(t, filepath.Join(repo, odd), "odd\n")
		_ = os.Remove(filepath.Join(repo, "gone.txt"))
	})
	after := readAll(t, repo)
	if c.Count != 4 {
		t.Fatalf("changes %+v", c)
	}
	ctx := context.Background()
	for _, f := range []string{odd, "新建/笔记.md", "gone.txt"} {
		if FilePatch(c.Patch, []string{f}) == "" {
			t.Fatalf("no section for %q in\n%s", f, c.Patch)
		}
	}
	if err := Apply(ctx, repo, FilePatch(c.Patch, []string{odd}), true); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for k, v := range after {
		want[k] = v
	}
	delete(want, odd)
	if got := readAll(t, repo); !same(got, want) {
		t.Fatalf("one file undo left %v", got)
	}
	if err := Apply(ctx, repo, PatchWithout(c.Patch, []string{odd}), true); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, repo); !same(got, before) {
		t.Fatalf("rest undo left %v, want %v", got, before)
	}
	if err := Apply(ctx, repo, FilePatch(c.Patch, []string{"gone.txt"}), false); err != nil {
		t.Fatal(err)
	}
	want = map[string]string{}
	for k, v := range before {
		want[k] = v
	}
	delete(want, "gone.txt")
	if got := readAll(t, repo); !same(got, want) {
		t.Fatalf("one file put back left %v", got)
	}
}
