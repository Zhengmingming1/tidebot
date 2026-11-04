package sessiontools

import (
	"fmt"
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

func TestSubpathDoesNotFollowDirectoryLinks(t *testing.T) {
	d := tree(t)
	outside := tree(t)
	testLink(t, outside, filepath.Join(d, "outside"))
	testLink(t, filepath.Join(d, "docs"), filepath.Join(d, "inside"))
	roots := []Root{{Label: "work", Dir: d}}
	for _, sub := range []string{"outside/docs", "outside/docs/a.md", "inside/deep", filepath.Join(d, "outside", "docs"), filepath.Join(d, "inside", "deep")} {
		t.Run(sub, func(t *testing.T) {
			if got, err := FindFiles(roots, "*", sub); err != nil || got != "No files match." {
				t.Errorf("find followed a link: %q, %v", got, err)
			}
			if got, err := SearchFiles(roots, "TODO", sub, "*", true); err != nil || got != "No matches." {
				t.Errorf("search followed a link: %q, %v", got, err)
			}
		})
	}
}

func TestWalkRootRejectsDirectoryReplacement(t *testing.T) {
	d := tree(t)
	outside := tree(t)
	testLink(t, outside, filepath.Join(d, "probe"))
	roots := []Root{{Label: "work", Dir: d}}
	visited := false
	err := walk(roots, "docs", "docs/a.md", func(_ Root, root *os.Root, rel string, _ int64) error {
		visited = true
		if err := os.Rename(filepath.Join(d, "docs"), filepath.Join(d, "saved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "docs"), filepath.Join(d, "docs")); err != nil {
			t.Fatal(err)
		}
		if f, err := root.Open(filepath.FromSlash(rel)); err == nil {
			f.Close()
			t.Fatal("read escaped through replacement directory")
		}
		return nil
	})
	if err != nil || !visited {
		t.Fatalf("walk: visited=%v err=%v", visited, err)
	}
}

func TestFindAndSearchExplicitFilesAndDotNames(t *testing.T) {
	d := tree(t)
	if err := os.WriteFile(filepath.Join(d, "..notes.md"), []byte("TODO dot name"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := []Root{{Label: "work", Dir: d}}
	for _, sub := range []string{"docs/a.md", filepath.Join(d, "docs", "a.md"), "..notes.md"} {
		if got, err := FindFiles(roots, "*.md", sub); err != nil || !strings.Contains(got, filepath.Base(sub)) {
			t.Fatalf("find %q: %s, %v", sub, got, err)
		}
		if got, err := SearchFiles(roots, "TODO", sub, "*.md", false); err != nil || !strings.Contains(got, "TODO") {
			t.Fatalf("search %q: %s, %v", sub, got, err)
		}
	}
}

func TestFindAndSearchRejectLinkedRoot(t *testing.T) {
	target := tree(t)
	link := filepath.Join(t.TempDir(), "root")
	testLink(t, target, link)
	roots := []Root{{Label: "work", Dir: link}}
	if got, err := FindFiles(roots, "*", ""); err != nil || got != "No files match." {
		t.Errorf("find followed linked root: %q, %v", got, err)
	}
	if got, err := SearchFiles(roots, "TODO", "", "*", false); err != nil || got != "No matches." {
		t.Errorf("search followed linked root: %q, %v", got, err)
	}
}

func testLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func TestRelativeDirectoryLink(t *testing.T) {
	d := tree(t)
	testLink(t, "docs", filepath.Join(d, "relative"))
	roots := []Root{{Label: "work", Dir: d}}
	if got, err := FindFiles(roots, "*", "relative/deep"); err != nil || got != "No files match." {
		t.Errorf("find: %q, %v", got, err)
	}
	if got, err := SearchFiles(roots, "TODO", "relative/deep", "*", true); err != nil || got != "No matches." {
		t.Errorf("search: %q, %v", got, err)
	}
}

func TestWalkRootRejectsFileReplacement(t *testing.T) {
	d := tree(t)
	outside := tree(t)
	// Probe privileges before entering the callback.
	testLink(t, outside, filepath.Join(d, "probe"))
	visited := false
	err := walk([]Root{{Label: "work", Dir: d}}, "docs/a.md", "*", func(_ Root, root *os.Root, rel string, _ int64) error {
		visited = true
		name := filepath.Join(d, filepath.FromSlash(rel))
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		testLink(t, filepath.Join(outside, "docs", "a.md"), name)
		if f, err := root.Open(filepath.FromSlash(rel)); err == nil {
			f.Close()
			t.Fatal("read escaped through replacement file")
		}
		return nil
	})
	if err != nil || !visited {
		t.Fatalf("walk: visited=%v err=%v", visited, err)
	}
}

func TestWalkKeepsOpenedRootAfterReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows may prevent renaming an open directory")
	}
	parent := t.TempDir()
	d := filepath.Join(parent, "root")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "a.md"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := tree(t)
	visited := false
	err := walk([]Root{{Label: "work", Dir: d}}, "a.md", "*", func(_ Root, root *os.Root, rel string, _ int64) error {
		visited = true
		if err := os.Rename(d, filepath.Join(parent, "saved")); err != nil {
			t.Fatal(err)
		}
		testLink(t, outside, d)
		b, err := root.ReadFile(filepath.FromSlash(rel))
		if err != nil || string(b) != "original" {
			t.Fatalf("lost original root: %q, %v", b, err)
		}
		return nil
	})
	if err != nil || !visited {
		t.Fatalf("walk: visited=%v err=%v", visited, err)
	}
}

func TestSearchFileAndTotalByteLimits(t *testing.T) {
	d := t.TempDir()
	roots := []Root{{Label: "work", Dir: d}}
	// Binary files count against the read budget even though their contents are skipped.
	b := make([]byte, maxFileBytes)
	for i := 0; i < maxTotalBytes/maxFileBytes; i++ {
		if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("%02d.bin", i)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "zz.md"), []byte("needle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := SearchFiles(roots, "needle", "", "*", false); err != nil || got != "No matches." {
		t.Fatalf("total budget: %q, %v", got, err)
	}
	if err := os.Remove(filepath.Join(d, "00.bin")); err != nil {
		t.Fatal(err)
	}
	if got, err := SearchFiles(roots, "needle", "", "*", false); err != nil || !strings.Contains(got, "zz.md:1: needle") {
		t.Fatalf("below total budget: %q, %v", got, err)
	}
	// The exact per-file boundary remains readable; one more byte must be skipped.
	text := strings.Repeat("a\n", maxFileBytes/2-4) + "needle\n!"
	if len(text) != maxFileBytes {
		t.Fatalf("fixture size %d", len(text))
	}
	if err := os.WriteFile(filepath.Join(d, "limit.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := SearchFiles(roots, "needle", "limit.md", "*", false); err != nil || !strings.Contains(got, "needle") {
		t.Fatalf("file at limit: %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(d, "limit.md"), []byte(text+"x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := SearchFiles(roots, "needle", "limit.md", "*", false); err != nil || got != "No matches." {
		t.Fatalf("file above limit: %q, %v", got, err)
	}
}
