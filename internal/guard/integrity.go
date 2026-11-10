package guard

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The after-run check of agent configuration (a backstop and an audit trail
// behind the deny rules and the sandbox): fingerprint the owner's Claude Code
// config and the agent config in the run's folders before a command run,
// compare after. A change is reported, never undone: putting files back would
// itself be tidebot changing the owner's config.

// FileState fingerprints one file.
type FileState struct {
	Size int64
	Mod  int64 // UnixNano
	Hash string
}

// ConfigSnapshot maps a path to its state; a missing path has no entry.
type ConfigSnapshot map[string]FileState

// ConfigChange is one path that changed during a run.
type ConfigChange struct {
	Path   string `json:"path"`
	Change string `json:"change"` // added | removed | modified
}

const (
	snapDirMax  = 2000    // files fingerprinted per config folder
	snapHashMax = 8 << 20 // larger files: size and time only
	snapUpMax   = 32      // parent folders looked at above a run folder
)

// homeConfigFiles are the owner's Claude Code files watched in ~/.claude.
// Not ~/.claude.json: the owner's own Claude Code sessions rewrite it all the
// time (state, counters), so it would raise false alarms; the sandbox keeps it
// read-only and the Edit deny rules cover the file tools. Never
// .credentials.json: the check doesn't read credentials.
var homeConfigFiles = []string{"settings.json", "settings.local.json", "CLAUDE.md"}

// homeConfigDirs are folders in ~/.claude whose files Claude Code loads as code or instructions.
var homeConfigDirs = []string{"skills", "agents", "commands", "hooks"}

// SnapshotAgentConfig fingerprints the owner's Claude Code config under home
// and the agent config in each run folder (folders and files the agents load)
// and, files only, in the folders above the first one (Claude Code reads
// CLAUDE.md and .claude/settings.json from parent folders too). skip lists
// folders that are tidebot's own (never fingerprinted).
func SnapshotAgentConfig(home string, dirs []string, skip []string) ConfigSnapshot {
	s := ConfigSnapshot{}
	skipped := func(p string) bool {
		for _, k := range skip {
			if k != "" && Within(p, k) {
				return true
			}
		}
		return false
	}
	if home != "" {
		cd := filepath.Join(home, ".claude")
		for _, f := range homeConfigFiles {
			s.file(filepath.Join(cd, f))
		}
		for _, d := range homeConfigDirs {
			s.dir(filepath.Join(cd, d))
		}
	}
	for i, d := range dirs {
		if d == "" || skipped(d) {
			continue
		}
		for _, n := range agentConfigDirs {
			s.dir(filepath.Join(d, n))
		}
		s.dir(filepath.Join(d, ".config", "opencode"))
		for _, n := range agentConfigFiles {
			s.file(filepath.Join(d, n))
		}
		if i != 0 {
			continue
		}
		up := d
		for n := 0; n < snapUpMax; n++ {
			parent := filepath.Dir(up)
			if parent == up {
				break
			}
			up = parent
			if skipped(up) || (home != "" && samePath(up, home)) {
				continue // the home folder's own config is covered above
			}
			for _, f := range agentConfigFiles {
				s.file(filepath.Join(up, f))
			}
			for _, f := range []string{"settings.json", "settings.local.json"} {
				s.file(filepath.Join(up, ".claude", f))
			}
		}
	}
	return s
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if caseFold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (s ConfigSnapshot) file(p string) {
	fi, err := os.Lstat(p)
	if err != nil || fi.IsDir() {
		return
	}
	st := FileState{Size: fi.Size(), Mod: fi.ModTime().UnixNano()}
	if fi.Mode().IsRegular() && fi.Size() <= snapHashMax && filepath.Base(p) != ".credentials.json" {
		if f, err := os.Open(p); err == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				st.Hash = hex.EncodeToString(h.Sum(nil))
			}
			f.Close()
		}
	} else if fi.Mode()&os.ModeSymlink != 0 {
		t, _ := os.Readlink(p)
		st.Hash = "link:" + t
	}
	s[p] = st
}

func (s ConfigSnapshot) dir(root string) {
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return fs.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if n++; n > snapDirMax {
			return fs.SkipAll
		}
		s.file(p)
		return nil
	})
}

// Diff lists what changed from before to after, sorted by path.
func (s ConfigSnapshot) Diff(after ConfigSnapshot) []ConfigChange {
	var out []ConfigChange
	for p, b := range s {
		a, ok := after[p]
		switch {
		case !ok:
			out = append(out, ConfigChange{Path: p, Change: "removed"})
		case a.Size != b.Size || a.Hash != b.Hash || (a.Hash == "" && a.Mod != b.Mod):
			out = append(out, ConfigChange{Path: p, Change: "modified"})
		}
	}
	for p := range after {
		if _, ok := s[p]; !ok {
			out = append(out, ConfigChange{Path: p, Change: "added"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
