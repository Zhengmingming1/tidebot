package sessiontools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for p, body := range map[string]string{
		"README.md": "hello tidebot\n", "docs/a.md": "alpha\nTODO: fix\n", "docs/deep/b.md": "beta todo\n",
		"src/main.go": "package main // TODO\n", ".git/config": "TODO secret\n", "bin.dat": "TODO\x00\x01",
	} {
		f := filepath.Join(d, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestFindAndSearchStayInside(t *testing.T) {
	d := tree(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "leak.md"), []byte("TODO outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(d, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "leak.md"), filepath.Join(d, "leak.md")); err != nil {
			t.Fatal(err)
		}
	}
	roots := []Root{{Label: "work", Dir: d}}
	got, err := FindFiles(roots, "*.md", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{filepath.Join(d, "README.md"), filepath.Join(d, "docs", "a.md"), filepath.Join(d, "docs", "deep", "b.md")} {
		if !strings.Contains(got, want) {
			t.Errorf("find lacks %s: %s", want, got)
		}
	}
	if strings.Contains(got, "leak") || strings.Contains(got, ".git") {
		t.Fatalf("find left the folder: %s", got)
	}
	if got, _ := FindFiles(roots, "docs/*.md", ""); strings.Contains(got, "b.md") || !strings.Contains(got, "a.md") {
		t.Fatalf("one level: %s", got)
	}
	if got, _ := FindFiles(roots, "*.{go,md}", "src"); !strings.Contains(got, "main.go") || strings.Contains(got, "a.md") {
		t.Fatalf("sub-folder: %s", got)
	}
	for _, bad := range []string{"..", "../x", "docs/../../x"} {
		if _, err := FindFiles(roots, "*", bad); err == nil {
			t.Errorf("path %q accepted", bad)
		}
	}
	if got, _ := FindFiles(roots, "*", outside); got != "No files match." {
		t.Fatalf("absolute outside path listed: %s", got)
	}
	s, err := SearchFiles(roots, "todo", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, filepath.Join(d, "docs", "a.md")+":2: TODO: fix") || !strings.Contains(s, "main.go:1:") || !strings.Contains(s, "b.md:1:") {
		t.Fatalf("search %s", s)
	}
	if strings.Contains(s, "secret") || strings.Contains(s, "outside") || strings.Contains(s, "bin.dat") {
		t.Fatalf("search read what it must not: %s", s)
	}
	if s, _ := SearchFiles(roots, "TODO", "", "*.go", false); strings.Contains(s, ".md") {
		t.Fatalf("glob filter: %s", s)
	}
	if _, err := SearchFiles(roots, "(", "", "", false); err == nil {
		t.Fatal("bad regexp accepted")
	}
}

func TestRootsRoundTrip(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "w"), filepath.Join(t.TempDir(), "s k")
	rs := ParseRoots(FormatRoots([]Root{{"work", a}, {"skills", b}}) + string(filepath.ListSeparator) + "bad=relative")
	if len(rs) != 2 || rs[0].Dir != a || rs[1].Dir != b {
		t.Fatalf("%+v", rs)
	}
}
