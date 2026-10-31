// Package gitsnap records what a run changed in a git work tree without touching
// the repository: no commit, stash, ref, index or object is written there.
//
// Start copies the repo's index into a scratch folder of tidebot's and runs
// `git add -A` + `git write-tree` against that copy, with new objects going to
// the scratch folder (GIT_OBJECT_DIRECTORY) and the repo's own objects read as an
// alternate. Finish does the same again on the scratch index (only files whose
// stat changed are hashed) and diffs the two trees. Close removes the scratch.
//
// Idea: cline/cline sdk/packages/core/src/hooks/checkpoint-hooks.ts keeps a
// per-session GIT_INDEX_FILE in its own data dir so unchanged files aren't
// re-hashed; tidebot also keeps the objects out of the repo, so nothing is left
// behind in the owner's .git.
package gitsnap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNotRepo: the folder is not inside a git work tree (or git isn't installed).
var ErrNotRepo = errors.New("not a git work tree")

// Limits of what is kept for the run page.
const (
	MaxFiles = 300       // files listed; the totals still count all of them
	MaxPatch = 512 << 10 // bytes of unified diff kept
	timeout  = 60 * time.Second
)

// Snap is a started snapshot.
type Snap struct {
	bin, dir, scratch string
	env               []string
	cfg               []string
	tree              string
}

// File is one changed path, relative to the run folder ("/" separated).
type File struct {
	Path   string `json:"path"`
	Status string `json:"status"` // A added | M modified | D deleted | T type changed
	Add    int    `json:"add"`
	Del    int    `json:"del"`
	Binary bool   `json:"binary,omitempty"`
}

// Changes is what Finish found.
type Changes struct {
	Files    []File `json:"files"`
	Count    int    `json:"count"` // all changed files (Files may stop at MaxFiles)
	Add      int    `json:"add"`
	Del      int    `json:"del"`
	Patch    string `json:"-"`
	PatchCut bool   `json:"patch_cut,omitempty"`
}

// Start snapshots dir's work tree (tracked and untracked, .gitignore honoured)
// into scratch, which must not exist yet or be empty, and must be outside dir.
func Start(ctx context.Context, dir, scratch string) (*Snap, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNotRepo
	}
	s := &Snap{bin: bin, dir: dir, scratch: scratch, env: baseEnv()}
	s.cfg = []string{
		"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.splitIndex=false",
		"-c", "core.hooksPath=" + os.DevNull, "-c", "core.quotePath=false", "-c", "core.safecrlf=false",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "index.threads=1",
	}
	out, err := s.git(ctx, nil, "rev-parse", "--is-inside-work-tree", "--git-common-dir", "--git-path", "index")
	if err != nil {
		return nil, ErrNotRepo
	}
	ln := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(ln) < 3 || strings.TrimSpace(ln[0]) != "true" {
		return nil, ErrNotRepo
	}
	common, index := s.abs(strings.TrimSpace(ln[1])), s.abs(strings.TrimSpace(ln[2]))
	// Filters (git-lfs and the like) would run their own programs and may write
	// into .git: snapshot the bytes on disk instead.
	if out, err := s.git(ctx, nil, "config", "--name-only", "--get-regexp", `^filter\.`); err == nil {
		seen := map[string]bool{}
		for _, k := range strings.Fields(string(out)) {
			i := strings.LastIndexByte(k, '.')
			if i <= len("filter.") || seen[k[:i]] {
				continue
			}
			seen[k[:i]] = true
			s.cfg = append(s.cfg, "-c", k[:i]+".clean=", "-c", k[:i]+".process=", "-c", k[:i]+".required=false")
		}
	}
	if err := os.MkdirAll(filepath.Join(scratch, "objects"), 0o700); err != nil {
		return nil, err
	}
	if err := copyFile(index, filepath.Join(scratch, "index")); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Close()
		return nil, err
	}
	s.env = append(s.env,
		"GIT_INDEX_FILE="+filepath.Join(scratch, "index"),
		"GIT_OBJECT_DIRECTORY="+filepath.Join(scratch, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(common, "objects"),
	)
	if s.tree, err = s.writeTree(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Finish snapshots the work tree again and returns what changed since Start
// (only under the run folder).
func (s *Snap) Finish(ctx context.Context) (*Changes, error) {
	end, err := s.writeTree(ctx)
	if err != nil {
		return nil, err
	}
	c := &Changes{Files: []File{}}
	if end == s.tree {
		return c, nil
	}
	diff := []string{"diff-tree", "-r", "--relative", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color"}
	out, err := s.git(ctx, nil, append(diff, "--raw", "--numstat", "-z", s.tree, end)...)
	if err != nil {
		return nil, err
	}
	parseRawNumstat(out, c)
	var pb limitBuf
	pb.max = MaxPatch
	// --binary: binary files too, so the patch can be applied (or undone) as a whole
	if _, err := s.git(ctx, &pb, append(diff, "-p", "--binary", "-U3", s.tree, end)...); err != nil && !pb.cut {
		return nil, err
	}
	c.Patch, c.PatchCut = pb.String(), pb.cut
	if c.PatchCut { // never end inside a line
		if i := strings.LastIndexByte(c.Patch, '\n'); i >= 0 {
			c.Patch = c.Patch[:i+1]
		}
	}
	return c, nil
}

// ErrConflict: the patch no longer applies — a file it touches was changed since.
// Message is git's own explanation (first lines), for the UI's tooltip.
type ErrConflict struct{ Message string }

func (e *ErrConflict) Error() string { return "patch does not apply: " + e.Message }

// Apply applies a patch Finish produced to dir's files (reverse: takes it back).
// git apply checks every hunk first and writes nothing unless all of them fit, so a
// file edited since the run leaves the folder untouched (*ErrConflict). Only the
// work tree is written: no index, ref or object, hooks and filter programs off,
// paths outside dir refused (git's default). dir need not be a repo root: the
// patch's paths are relative to it (diff-tree --relative).
func Apply(ctx context.Context, dir, patch string, reverse bool) error {
	bin, err := exec.LookPath("git")
	if err != nil {
		return ErrNotRepo
	}
	s := &Snap{bin: bin, dir: dir, env: baseEnv()}
	s.cfg = []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "core.quotePath=false",
		"-c", "core.safecrlf=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	// the snapshots read the bytes on disk with filter programs off: do the same here
	if out, err := s.git(ctx, nil, "config", "--name-only", "--get-regexp", `^filter\.`); err == nil {
		seen := map[string]bool{}
		for _, k := range strings.Fields(string(out)) {
			i := strings.LastIndexByte(k, '.')
			if i <= len("filter.") || seen[k[:i]] {
				continue
			}
			seen[k[:i]] = true
			s.cfg = append(s.cfg, "-c", k[:i]+".clean=", "-c", k[:i]+".smudge=", "-c", k[:i]+".process=", "-c", k[:i]+".required=false")
		}
	}
	args := []string{"apply", "--whitespace=nowarn"}
	// Run from the top of the work tree with the run folder as --directory: started in
	// a subfolder, git apply would read the paths from the top and quietly skip them.
	top := dir
	if out, err := s.git(ctx, nil, "rev-parse", "--show-toplevel", "--show-prefix"); err == nil {
		ln := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
		if len(ln) == 2 && strings.TrimSpace(ln[0]) != "" {
			top = filepath.FromSlash(strings.TrimSpace(ln[0]))
			if pre := strings.Trim(strings.TrimSpace(ln[1]), "/"); pre != "" {
				args = append(args, "--directory="+pre)
			}
		}
	}
	if reverse {
		args = append(args, "-R")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, append(append([]string{}, s.cfg...), append(args, "-")...)...)
	cmd.Dir, cmd.Env, cmd.Stdin = top, s.env, strings.NewReader(patch)
	errb := limitBuf{max: 4 << 10, drop: true}
	cmd.Stdout, cmd.Stderr = io.Discard, &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &ee) {
			return &ErrConflict{Message: firstLines(errb.String(), 4)}
		}
		return fmt.Errorf("git apply: %w", err)
	}
	return nil
}

func firstLines(s string, n int) string {
	ln := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")), "\n")
	if len(ln) > n {
		ln = ln[:n]
	}
	return strings.Join(ln, "\n")
}

// Close removes the scratch folder.
func (s *Snap) Close() {
	if s != nil && s.scratch != "" {
		_ = os.RemoveAll(s.scratch)
	}
}

func (s *Snap) writeTree(ctx context.Context) (string, error) {
	// "." is the run folder: a subfolder of a bigger repo only scans itself.
	if _, err := s.git(ctx, nil, "add", "-A", "--ignore-errors", "--", "."); err != nil {
		// --ignore-errors still exits 1 when an unreadable file was skipped; the
		// index has everything else, so go on.
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return "", err
		}
	}
	out, err := s.git(ctx, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Snap) git(ctx context.Context, w io.Writer, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, append(append([]string{}, s.cfg...), args...)...)
	cmd.Dir = s.dir
	cmd.Env = s.env
	var out bytes.Buffer
	errb := limitBuf{max: 4 << 10, drop: true}
	cmd.Stdout, cmd.Stderr = &out, &errb
	if w != nil {
		cmd.Stdout = w
	}
	if err := cmd.Run(); err != nil {
		if lb, ok := w.(*limitBuf); ok && lb.cut {
			return nil, nil
		}
		return out.Bytes(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func (s *Snap) abs(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(s.dir, p)
}

// baseEnv: the process environment without GIT_* variables that would point
// git somewhere else, plus no prompts, no optional locks and C messages.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if strings.HasPrefix(strings.ToUpper(k), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

// parseRawNumstat reads `--raw --numstat -z` output: first every raw record
// (":old new oldsha newsha S\0path\0"), then every numstat ("add\tdel\tpath\0";
// "-\t-\t" for binary files).
func parseRawNumstat(b []byte, c *Changes) {
	parts := strings.Split(string(b), "\x00")
	idx := map[string]int{}
	var all []File
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		switch {
		case strings.HasPrefix(p, ":"):
			f := strings.Fields(p)
			if len(f) < 5 || i+1 >= len(parts) {
				continue
			}
			i++
			idx[parts[i]] = len(all)
			all = append(all, File{Path: parts[i], Status: f[4][:1]})
		case strings.Count(p, "\t") >= 2:
			f := strings.SplitN(p, "\t", 3)
			j, ok := idx[f[2]]
			if !ok {
				continue
			}
			if f[0] == "-" {
				all[j].Binary = true
				continue
			}
			all[j].Add, _ = strconv.Atoi(f[0])
			all[j].Del, _ = strconv.Atoi(f[1])
		}
	}
	c.Count = len(all)
	for _, f := range all {
		c.Add += f.Add
		c.Del += f.Del
	}
	if len(all) > MaxFiles {
		all = all[:MaxFiles]
	}
	c.Files = append(c.Files[:0], all...)
}

// limitBuf keeps the first max bytes and then reports a write error, which
// makes git stop early (EPIPE) instead of producing a huge diff.
// (The buffer is a field, not embedded: an embedded bytes.Buffer's ReadFrom
// would let io.Copy bypass Write.)
type limitBuf struct {
	b   bytes.Buffer
	max int
	cut bool
	// drop: past max, discard quietly instead of failing the write (stderr:
	// a chatty git must not die of a closed pipe).
	drop bool
}

func (l *limitBuf) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); len(p) > room {
		if room > 0 {
			l.b.Write(p[:room])
		}
		l.cut = true
		if l.drop {
			return len(p), nil
		}
		return 0, io.ErrShortWrite
	}
	return l.b.Write(p)
}

func (l *limitBuf) String() string { return l.b.String() }

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
