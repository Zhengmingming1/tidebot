package skillstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryImportRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("fixture-sensitive-content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := readTree(dir); err == nil {
		t.Fatal("import followed symlink to private file")
	}
}
