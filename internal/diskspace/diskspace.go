// Package diskspace reports free and total space on the volume holding a path.
// macOS/Linux use statfs, Windows uses GetDiskFreeSpaceEx; other platforms
// report ErrUnsupported so callers can skip the check.
package diskspace

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrUnsupported is returned where the OS has no implementation.
var ErrUnsupported = errors.New("diskspace: not supported on this platform")

// Info is the space on one volume. Free is what the current user can write
// (excludes blocks reserved for root on Linux), which is what matters for us.
type Info struct {
	Free  uint64 `json:"free_bytes"`
	Total uint64 `json:"total_bytes"`
}

// Of returns the space on the volume holding path. path must exist; a file is
// measured through its folder (Windows only accepts directories).
func Of(path string) (Info, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Info{}, err
	}
	if !fi.IsDir() {
		path = filepath.Dir(path)
	}
	return of(path)
}
