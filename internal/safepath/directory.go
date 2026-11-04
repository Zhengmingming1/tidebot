// Package safepath supports directory guards without following user-controlled links.
package safepath

import (
	"os"
	"path/filepath"
	"runtime"
)

// LstatDirectory behaves like os.Lstat except for macOS's fixed system aliases
// /var, /tmp and /etc. Their exact /private targets and parent must be real
// directories. User-created links, including descendants of these aliases,
// remain links so callers can reject them.
func LstatDirectory(name string) (os.FileInfo, error) {
	info, err := os.Lstat(name)
	if err != nil || runtime.GOOS != "darwin" || info.Mode()&os.ModeSymlink == 0 {
		return info, err
	}
	clean := filepath.Clean(name)
	target := ""
	switch clean {
	case "/var":
		target = "/private/var"
	case "/tmp":
		target = "/private/tmp"
	case "/etc":
		target = "/private/etc"
	default:
		return info, nil
	}
	link, e := os.Readlink(clean)
	if e != nil {
		return nil, e
	}
	if link != target && link != target[1:] {
		return info, nil
	}
	parent, e := os.Lstat("/private")
	if e != nil {
		return nil, e
	}
	if !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return info, nil
	}
	resolved, e := os.Lstat(target)
	if e != nil {
		return nil, e
	}
	if !resolved.IsDir() || resolved.Mode()&os.ModeSymlink != 0 {
		return info, nil
	}
	return resolved, nil
}
