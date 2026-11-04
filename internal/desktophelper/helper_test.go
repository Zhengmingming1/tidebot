package desktophelper

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS helper executable permissions require POSIX")
	}
	root := t.TempDir()
	exe := filepath.Join(root, "Tidebot.app", "Contents", "MacOS", "tidebot-desktop")
	helper := filepath.Join(root, "Tidebot.app", "Contents", "Helpers", "tidebot-helper")
	for _, platform := range []string{"linux", "windows"} {
		if got, err := Executable(exe, platform); err != nil || got != exe {
			t.Fatalf("%s: %q %v", platform, got, err)
		}
	}
	if got, err := Executable(filepath.Join(root, "tidebot-desktop"), "darwin"); err != nil || got == "" {
		t.Fatalf("development: %q %v", got, err)
	}
	if _, err := Executable(exe, "darwin"); err == nil || !strings.Contains(err.Error(), "reinstall") {
		t.Fatalf("missing helper must not re-exec GUI: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(helper), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("headless test helper"), 0755); err != nil {
		t.Fatal(err)
	}
	if got, err := Executable(exe, "darwin"); err != nil || got != helper {
		t.Fatalf("bundle: %q %v", got, err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("gui test executable"), 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "tidebot-alias")
	if err := os.Symlink(exe, alias); err != nil {
		t.Fatal(err)
	}
	resolvedHelper, err := filepath.EvalSymlinks(helper)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Executable(alias, "darwin"); err != nil || got != resolvedHelper {
		t.Fatalf("symlink: %q %v", got, err)
	}
	if err := os.Chmod(helper, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Executable(exe, "darwin"); err == nil {
		t.Fatal("non-executable helper accepted")
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, helper); err != nil {
		t.Fatal(err)
	}
	if _, err := Executable(exe, "darwin"); err == nil {
		t.Fatal("helper symlink to GUI accepted")
	}
}
