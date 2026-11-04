// Package desktophelper locates the non-GUI worker shipped in a macOS app.
package desktophelper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Executable keeps the single-binary development/non-macOS path, but never
// silently falls back to launching the GUI inside a packaged macOS application.
func Executable(exe, platform string) (string, error) {
	// os.Executable may retain a symlink path on some platforms. Resolve it so
	// launching through an alias still selects the helper beside the real app.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	contents := filepath.Dir(dir)
	if platform != "darwin" || filepath.Base(dir) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(filepath.Dir(contents), ".app") {
		return exe, nil
	}
	helper := filepath.Join(contents, "Helpers", "tidebot-helper")
	info, err := os.Lstat(helper)
	if err != nil {
		return "", fmt.Errorf("desktop helper missing; reinstall Tidebot: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("desktop helper is not executable; reinstall Tidebot")
	}
	return helper, nil
}
