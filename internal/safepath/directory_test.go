package safepath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirectoryGuardKeepsUserLinksVisible(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	info, err := LstatDirectory(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("user link was followed: %v %v", info, err)
	}
	info, err = LstatDirectory(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("regular directory rejected: %v", err)
	}
}
func TestDarwinSystemDirectoryAliases(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system aliases")
	}
	for _, p := range []string{"/var", "/tmp", "/etc"} {
		t.Run(p, func(t *testing.T) {
			info, err := LstatDirectory(p)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("system directory rejected: %v %v", info, err)
			}
		})
	}
}
