package osopen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	p := "/Users/a b/报告 1.csv"
	if g := Commands("darwin", p, true); !reflect.DeepEqual(g, []Candidate{{Name: "open", Args: []string{"-R", p}}}) {
		t.Fatalf("darwin %v", g)
	}
	if g := Commands("windows", `C:\x y\a.csv`, true); g[0].Name != "explorer.exe" || g[0].Args[0] != `/select,C:\x y\a.csv` || len(g[0].Args) != 1 {
		t.Fatalf("windows %v", g)
	}
	if g := Commands("windows", `C:\data`, false); g[0].Args[0] != `C:\data` {
		t.Fatalf("windows dir %v", g)
	}
	g := Commands("linux", p, true)
	if len(g) != 2 || g[0].Name != "dbus-send" || !g[0].Wait || g[1].Name != "xdg-open" || g[1].Args[0] != "/Users/a b" {
		t.Fatalf("linux %v", g)
	}
	if u := g[0].Args[6]; !strings.HasPrefix(u, "array:string:file:///Users/a%20b/") {
		t.Fatalf("uri %q", u)
	}
	if g := Commands("freebsd", "/d", false); len(g) != 1 || g[0].Name != "xdg-open" {
		t.Fatalf("bsd %v", g)
	}
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.csv")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	var got []Candidate
	fails := 0
	orig := run
	run = func(c Candidate) error {
		got = append(got, c)
		if fails > 0 {
			fails--
			return errors.New("no")
		}
		return nil
	}
	t.Cleanup(func() { run = orig })
	if err := Open(filepath.Join(dir, "missing"), true); !errors.Is(err, ErrNotFound) || len(got) != 0 {
		t.Fatalf("missing %v %v", err, got)
	}
	if err := Open(dir, true); err != nil || len(got) != 1 {
		t.Fatalf("dir %v %v", err, got)
	}
	last := got[0].Args[len(got[0].Args)-1]
	if last != dir && last != "/select,"+dir && !strings.HasSuffix(last, filepath.Base(dir)) {
		t.Fatalf("dir opened as %v", got[0])
	}
	got = nil
	if runtime.GOOS == "linux" {
		fails = 1 // no FileManager1 on the bus: the xdg-open fallback is tried
	}
	if err := Open(f, true); err != nil {
		t.Fatalf("file %v", err)
	}
	if len(got) == 0 {
		t.Fatal("nothing run")
	}
}
