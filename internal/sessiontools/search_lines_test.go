package sessiontools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Windows (CRLF) files keep matching `$` and show no stray \r (as bufio.ScanLines did).
func TestSearchCRLFLines(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "win.go", "package main\r\n\r\nfunc main() {\r\n}\r\n")
	got, err := SearchFiles([]Root{{Label: "work", Dir: d}}, `^func .*\{$`, "", "", false)
	if err != nil || !strings.Contains(got, "win.go:3: func main() {") {
		t.Fatalf("CRLF $: %q, %v", got, err)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("stray \\r in %q", got)
	}
	// a lone \r inside a line is kept; only the line ending is dropped
	writeFile(t, d, "mac.txt", "a\rneedle\n")
	if got, _ := SearchFiles([]Root{{Label: "work", Dir: d}}, "needle", "", "*.txt", false); !strings.Contains(got, "mac.txt:1: a\rneedle") {
		t.Fatalf("inner \\r: %q", got)
	}
}

// A long Chinese line is cut on a character boundary, not into U+FFFD.
func TestSearchClipsOnCharacterBoundary(t *testing.T) {
	d := t.TempDir()
	// "xx" shifts every 3-byte character so that byte 400 falls inside one
	writeFile(t, d, "zh.md", "xx"+strings.Repeat("潮汐", 200)+" needle\n")
	got, err := SearchFiles([]Root{{Label: "work", Dir: d}}, "needle", "", "", false)
	if err != nil || !strings.Contains(got, "zh.md:1: xx潮汐") {
		t.Fatalf("search: %q, %v", got, err)
	}
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("clipped line is not valid UTF-8 or not marked: %q", got[len(got)-20:])
	}
	_, line, _ := strings.Cut(got, ":1: ")
	if n := len(strings.TrimSuffix(line, "…")); n > maxLineBytes || n < maxLineBytes-3 {
		t.Fatalf("clipped to %d bytes", n)
	}
}

// A line longer than the old 1 MiB scanner buffer no longer ends the search
// of the file; later lines are still found.
func TestSearchAfterVeryLongLine(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "min.js", strings.Repeat("a", 1<<20+10)+"\nvar needle = 1;\n")
	got, err := SearchFiles([]Root{{Label: "work", Dir: d}}, "needle", "", "", false)
	if err != nil || !strings.Contains(got, "min.js:2: var needle = 1;") {
		t.Fatalf("after long line: %q, %v", got, err)
	}
}

// The literal-prefix skip never hides a match: alternations, case folding,
// anchors and a prefix that only appears in another line form.
func TestSearchPrefilterKeepsMatches(t *testing.T) {
	d := t.TempDir()
	writeFile(t, d, "a.txt", "one\nTODO: Fix\nlast line")
	roots := []Root{{Label: "work", Dir: d}}
	for _, c := range []struct {
		pat  string
		ic   bool
		want string
	}{
		{"TODO|FIXME", false, "a.txt:2: TODO: Fix"},
		{"todo", true, "a.txt:2: TODO: Fix"},
		{"^TODO: F", false, "a.txt:2: TODO: Fix"},
		{"line$", false, "a.txt:3: last line"},
		{"Fi[x]", false, "a.txt:2: TODO: Fix"},
	} {
		if got, err := SearchFiles(roots, c.pat, "", "", c.ic); err != nil || !strings.Contains(got, c.want) {
			t.Fatalf("%q: %q, %v", c.pat, got, err)
		}
	}
	if got, _ := SearchFiles(roots, "TODO: Fox", "", "", false); got != "No matches." {
		t.Fatalf("absent literal: %q", got)
	}
}

func TestEachLine(t *testing.T) {
	var got []string
	eachLine([]byte("a\r\nb\n\nc"), func(n int, l []byte) bool {
		got = append(got, string(l))
		return n < 3
	})
	if strings.Join(got, "|") != "a|b|" {
		t.Fatalf("lines %q", got)
	}
}

func TestReadCappedReusesBuffer(t *testing.T) {
	buf := make([]byte, 0, 16)
	b, keep, err := readCapped(strings.NewReader("hello world, this is longer"), 11, buf)
	if err != nil || string(b) != "hello world" || &keep[:1][0] != &buf[:1][0] {
		t.Fatalf("read %q, %v (buffer reused: %v)", b, err, &keep[:1][0] == &buf[:1][0])
	}
	b, _, _ = readCapped(strings.NewReader(strings.Repeat("z", 40)), 100, keep)
	if len(b) != 40 {
		t.Fatalf("grow: %d", len(b))
	}
}
