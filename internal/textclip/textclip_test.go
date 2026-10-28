package textclip

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHead(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 3, "abc"},
		{"abc", 2, "ab"},
		{"abc", 0, ""},
		{"中文", 3, "中"},
		{"中文", 4, "中"},
		{"中文", 5, "中"},
		{"中文", 6, "中文"},
		{"a😀b", 2, "a"},
		{"a😀b", 5, "a😀"},
		{"\xff\xfeab", 1, "\xff"}, // not UTF-8 to begin with: still cut, never grown
	} {
		if got := Head(c.s, c.n); got != c.want {
			t.Errorf("Head(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
	// every cut of a mixed string stays valid and within budget
	s := strings.Repeat("提示词 prompt ✓ 😀 ", 40)
	for n := 0; n <= len(s); n++ {
		h := Head(s, n)
		if len(h) > n || !utf8.ValidString(h) || !strings.HasPrefix(s, h) || n-len(h) > 3 {
			t.Fatalf("n=%d: %q", n, h)
		}
	}
}

func TestEllipsis(t *testing.T) {
	if got := Ellipsis("短", 300); got != "短" {
		t.Fatal(got)
	}
	if got := Ellipsis(strings.Repeat("中", 101), 300); got != strings.Repeat("中", 100)+"…" {
		t.Fatal(got)
	}
	if got := Ellipsis(strings.Repeat("中", 101), 301); got != strings.Repeat("中", 100)+"…" {
		t.Fatal(got)
	}
}
