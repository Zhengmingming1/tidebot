package skillpkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortablePathsRejectDeviceAndAliasNames(t *testing.T) {
	for _, name := range []string{"a:stream", "dir/NUL.txt", "CON", "a/COM1.py", "a/LPT9", "file.", "file ", "a\x00b", "a?b"} {
		if _, err := CleanRel(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"references/a.md", "技能/SKILL.md", "a-b.py"} {
		if _, err := CleanRel(name); err != nil {
			t.Error(err)
		}
	}
}
func TestExtractionCannotFollowExistingDirectorySymlink(t *testing.T) {
	dest, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, "escape")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	err := WriteFiles([]FileEntry{{Path: "escape/created.txt", Data: strings.NewReader("private")}}, dest)
	if err == nil {
		t.Fatal("followed destination symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "created.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote outside extraction root", err)
	}
}
func TestTarDirectoryEntriesAreBounded(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i < MaxEntries+1; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	if err := Extract("dirs.tgz", buf.Bytes(), t.TempDir()); err == nil {
		t.Fatal("unbounded directory entries")
	}
}
