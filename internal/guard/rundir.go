package guard

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Problems CheckRunDir reports (the WebUI shows them in the viewer's language).
const (
	DirRelative          = "relative"
	DirNotFound          = "not_found"
	DirNotDir            = "not_dir"
	DirRoot              = "root"
	DirSensitive         = "sensitive"          // inside a folder with keys, credentials or system files
	DirContainsSensitive = "contains_sensitive" // e.g. the home folder itself: it holds ~/.ssh
	DirInternal          = "internal"           // tidebot's own folder
)

// sensitiveHome are folders under the user's home a run never works in: keys,
// cloud and tool credentials, and Claude Code's own configuration.
var sensitiveHome = []string{
	".ssh", ".aws", ".gnupg", ".claude", ".config/gh", ".kube", ".docker",
	".config/gcloud", ".azure", ".password-store", ".netrc", ".git-credentials",
	"Library/Keychains",
}

// sensitiveSystem are system folders a run never works in.
func sensitiveSystem() []string {
	if runtime.GOOS == "windows" {
		var out []string
		for _, v := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
			if p := os.Getenv(v); p != "" {
				out = append(out, p)
			}
		}
		return append(out, `C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\ProgramData`)
	}
	if runtime.GOOS == "darwin" { // not /private/var as a whole: /var/folders is the temp folder
		return []string{"/System", "/Library", "/usr", "/bin", "/sbin", "/etc", "/private/etc",
			"/private/var/db", "/private/var/root", "/private/var/log", "/dev", "/cores", "/opt/homebrew/etc"}
	}
	return []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys",
		"/root", "/var/lib", "/var/log", "/var/spool", "/var/db"}
}

// CheckRunDir validates a folder a run may work in and returns its real path
// (symlinks resolved first, so a link can't point a run somewhere it may not
// go). Any existing folder is allowed except a filesystem root, tidebot's own
// folder, and folders with keys, credentials or system files (or a folder that
// contains one of those, like the home folder itself).
func CheckRunDir(p, userHome, appHome string) (string, string) {
	if !filepath.IsAbs(p) {
		return "", DirRelative
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", DirNotFound
	}
	real, _ = filepath.Abs(real)
	fi, err := os.Stat(real)
	if err != nil {
		return "", DirNotFound
	}
	if !fi.IsDir() {
		return "", DirNotDir
	}
	if filepath.Dir(real) == real {
		return "", DirRoot
	}
	var deny []string
	homes := []string{userHome}
	if r, err := filepath.EvalSymlinks(userHome); userHome != "" && err == nil && r != userHome {
		homes = append(homes, r) // e.g. a Windows 8.3 short name or a linked home
	}
	for _, h := range homes {
		if h == "" {
			continue
		}
		for _, s := range sensitiveHome {
			deny = append(deny, filepath.Join(h, filepath.FromSlash(s)))
		}
	}
	deny = append(deny, sensitiveSystem()...)
	for _, d := range expand(deny) {
		if Within(real, d) {
			return "", DirSensitive
		}
		if Within(d, real) {
			return "", DirContainsSensitive
		}
	}
	if appHome != "" {
		for _, d := range expand([]string{appHome, filepath.Join(appHome, "data")}) {
			if Within(real, d) || Within(d, real) {
				return "", DirInternal
			}
		}
	}
	return real, ""
}

// expand adds each path's real path (e.g. /etc -> /private/etc on macOS).
func expand(paths []string) []string {
	out := make([]string, 0, len(paths)*2)
	for _, p := range paths {
		p = filepath.Clean(p)
		out = append(out, p)
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			out = append(out, r)
		}
	}
	return out
}

var caseFold = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// Within reports whether p is dir or inside it (case-insensitive on macOS and Windows).
func Within(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	if caseFold {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// DirError is a working folder CheckRunDir refused.
type DirError struct{ Problem string }

func (e *DirError) Error() string { return "working folder not allowed: " + e.Problem }

// ErrDirBusy: a run that may change files is already going in that folder.
var ErrDirBusy = errors.New("working folder busy: a run that can change files is already going there")

// ExpandHome turns "~" / "~/x" / "~\x" into a path under the user's home and
// drops quotes pasted around a path (Explorer's "Copy as path").
func ExpandHome(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 && (p[0] == '"' && p[len(p)-1] == '"' || p[0] == '\'' && p[len(p)-1] == '\'') {
		p = strings.TrimSpace(p[1 : len(p)-1])
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}
