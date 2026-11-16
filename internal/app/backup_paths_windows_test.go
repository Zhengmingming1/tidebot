//go:build windows

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// On a real Windows volume, TempDir is often the 8.3 form (C:\Users\RUNNER~1\...)
// while GetLongPathName yields C:\Users\runneradmin\.... Remap must treat them
// as the same prefix; this is what broke CI when a backup stored one form and
// the manifest the other.
func TestRemapPathRealShortAndLongTempDir(t *testing.T) {
	home := t.TempDir()
	short, long, err := shortAndLongPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.Clean(short), filepath.Clean(long)) {
		t.Skip("temp dir has no 8.3 alias on this volume")
	}
	nh := filepath.Join("new", "home")
	want := filepath.Join(nh, "workspaces", "t1")
	p := filepath.Join(short, "workspaces", "t1")
	if got, ok := remapPath(p, []homeMove{{long, nh}}); !ok || got != want {
		t.Errorf("short path with long prefix: remapPath(%q, old=%q) = %q,%v want %q", p, long, got, ok, want)
	}
	p = filepath.Join(long, "workspaces", "t1")
	if got, ok := remapPath(p, []homeMove{{short, nh}}); !ok || got != want {
		t.Errorf("long path with short prefix: remapPath(%q, old=%q) = %q,%v want %q", p, short, got, ok, want)
	}
}

func shortAndLongPath(p string) (short, long string, err error) {
	short, err = winPathName(p, windows.GetShortPathName)
	if err != nil {
		return "", "", err
	}
	long, err = winPathName(p, windows.GetLongPathName)
	return short, long, err
}

func winPathName(p string, get func(*uint16, *uint16, uint32) (uint32, error)) (string, error) {
	src, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := get(src, &buf[0], uint32(len(buf)))
		if n == 0 {
			return "", err
		}
		if n < uint32(len(buf)) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}
