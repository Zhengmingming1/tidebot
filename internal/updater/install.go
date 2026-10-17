package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// How this copy was installed decides whether we may replace it.
const (
	ModeApply  = "apply"  // we own the files: replace + restart
	ModeNotify = "notify" // package manager / container / service image: tell the user only
)

// Install describes the running copy.
type Install struct {
	Product string `json:"product"` // server | desktop
	Kind    string `json:"kind"`    // tar | zip | appimage | app | nsis | deb | docker | homebrew | managed
	Mode    string `json:"mode"`
	Target  string `json:"target"` // file or .app dir that gets replaced
	Reason  string `json:"reason,omitempty"`
}

// Detect inspects exe (os.Executable) for product ("server" or "desktop").
func Detect(product, exe string) Install {
	in := Install{Product: product, Target: exe, Mode: ModeApply}
	notify := func(kind, why string) Install { in.Kind, in.Mode, in.Reason = kind, ModeNotify, why; return in }
	if os.Getenv("TIDEBOT_MANAGED") == "1" {
		return notify("managed", "TIDEBOT_MANAGED=1")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return notify("docker", "running in a container; pull the new image")
	}
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/"):
		return notify("homebrew", "installed by Homebrew")
	case strings.HasPrefix(p, "/nix/store/"):
		return notify("nix", "installed by Nix")
	}
	if product == "desktop" {
		switch {
		case os.Getenv("APPIMAGE") != "":
			in.Kind, in.Target = "appimage", os.Getenv("APPIMAGE")
		case runtime.GOOS == "darwin":
			if i := strings.Index(p, ".app/Contents/MacOS/"); i > 0 {
				in.Kind, in.Target = "app", filepath.FromSlash(p[:i+4])
			} else {
				return notify("unknown", "not inside a .app bundle")
			}
		case runtime.GOOS == "windows":
			in.Kind = "zip" // files are renamed aside while running, then replaced
		default:
			return notify("deb", "installed from the .deb; use your package manager")
		}
		return in
	}
	if strings.HasPrefix(p, "/usr/bin/") || strings.HasPrefix(p, "/usr/sbin/") {
		return notify("deb", "installed from a package; use your package manager")
	}
	in.Kind = "tar"
	if runtime.GOOS == "windows" {
		in.Kind = "zip"
	}
	return in
}

// Check is the user-facing result of a check.
type Check struct {
	Current   string  `json:"current"`
	Latest    string  `json:"latest"`
	Available bool    `json:"available"`
	Date      string  `json:"date,omitempty"`
	Notes     string  `json:"notes,omitempty"`
	NotesURL  string  `json:"notes_url,omitempty"`
	Install   Install `json:"install"`
	CanApply  bool    `json:"can_apply"`
	CheckedAt string  `json:"checked_at"`
	asset     *Asset
	manifest  *Manifest
}

// Evaluate compares a verified manifest with the running build.
func Evaluate(m *Manifest, current string, in Install, goos, goarch string) *Check {
	c := &Check{Current: current, Latest: strings.TrimPrefix(m.Version, "v"), Date: m.Date, Notes: m.Notes, NotesURL: m.NotesURL,
		Install: in, CheckedAt: time.Now().UTC().Format(time.RFC3339), manifest: m}
	c.Available = current != "dev" && CompareVersions(c.Latest, current) > 0
	if a, ok := m.Assets[AssetKey(in.Product, goos, goarch)]; ok {
		c.asset = &a
	}
	c.CanApply = c.Available && in.Mode == ModeApply && c.asset != nil
	return c
}

// CompareVersions compares dotted numeric versions (pre-release suffix ignored).
func CompareVersions(x, y string) int {
	px := strings.Split(strings.SplitN(strings.TrimPrefix(x, "v"), "-", 2)[0], ".")
	py := strings.Split(strings.SplitN(strings.TrimPrefix(y, "v"), "-", 2)[0], ".")
	for i := 0; i < 3; i++ {
		var a, b int
		if i < len(px) {
			fmt.Sscanf(px[i], "%d", &a)
		}
		if i < len(py) {
			fmt.Sscanf(py[i], "%d", &b)
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Pending is the marker written before restarting into a new build.
type Pending struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Swaps    []Swap `json:"swaps"`
	Attempts int    `json:"attempts"`
	At       string `json:"at"`
}

// Swap is one replaced path and its backup.
type Swap struct {
	Path string `json:"path"`
	Prev string `json:"prev"`
}

// Result is the last finished update (shown in Settings).
type Result struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"` // ok | rolled_back | failed
	Detail string `json:"detail,omitempty"`
	At     string `json:"at"`
}

func pendingPath(state string) string { return filepath.Join(state, "update-pending.json") }
func resultPath(state string) string  { return filepath.Join(state, "update-result.json") }

// MaxAttempts: starts allowed for a new build before we roll back.
const MaxAttempts = 2

// Apply downloads the asset, verifies sha256, swaps files (keeping one .prev
// backup) and writes the pending marker. For nsis it only stages the installer
// and returns its path; the caller runs it.
func Apply(ctx context.Context, hc *http.Client, c *Check, stateDir string) (installer string, err error) {
	return ApplyFrom(ctx, hc, c, stateDir, nil)
}

// ApplyFrom is Apply with a list of sources tried in order for the download;
// a file that fails the sha256 check counts as a failed source.
func ApplyFrom(ctx context.Context, hc *http.Client, c *Check, stateDir string, srcs []Source) (installer string, err error) {
	if !c.CanApply || c.asset == nil {
		return "", errors.New("no applicable update")
	}
	if len(srcs) == 0 {
		srcs = []Source{Direct}
	}
	var data []byte
	var lastErr error
	fe := &FetchError{}
	for _, s := range srcs {
		u := s.AssetURL(c.asset.URL, c.Latest)
		if s.Name == "env" {
			u = c.asset.URL
		}
		b, gerr := get(ctx, hc, u, 400<<20)
		if gerr == nil {
			gerr = checkSum(b, c.asset.SHA256)
		}
		if gerr == nil {
			data = b
			break
		}
		lastErr = gerr
		fe.Attempts = append(fe.Attempts, Attempt{Source: s.Name, Code: Classify(gerr), Error: gerr.Error()})
		if ctx.Err() != nil {
			break
		}
	}
	if data == nil {
		if len(fe.Attempts) == 1 {
			return "", lastErr
		}
		return "", fe
	}
	in := c.Install
	var swaps []Swap
	switch c.asset.Kind {
	case "server-tar", "server-zip", "desktop-zip":
		bins, err := extractServer(c.asset.Kind, data)
		if err != nil {
			return "", err
		}
		dir := filepath.Dir(in.Target)
		for name, b := range bins {
			sw, err := swapFile(filepath.Join(dir, name), b)
			if err != nil {
				rollbackSwaps(swaps)
				return "", err
			}
			swaps = append(swaps, sw)
		}
		if len(swaps) == 0 {
			return "", errors.New("archive has no tidebot binaries")
		}
	case "appimage":
		sw, err := swapFile(in.Target, data)
		if err != nil {
			return "", err
		}
		swaps = append(swaps, sw)
	case "app-tar":
		sw, err := swapAppBundle(in.Target, data)
		if err != nil {
			return "", err
		}
		swaps = append(swaps, sw)
	case "nsis":
		f := filepath.Join(os.TempDir(), "tidebot-setup-"+c.Latest+".exe")
		if err := os.WriteFile(f, data, 0o700); err != nil {
			return "", err
		}
		return f, nil
	default:
		return "", fmt.Errorf("unknown asset kind %q", c.asset.Kind)
	}
	p := Pending{From: c.Current, To: c.Latest, Kind: c.asset.Kind, Swaps: swaps, At: time.Now().UTC().Format(time.RFC3339)}
	if err := writeJSON(pendingPath(stateDir), p); err != nil {
		rollbackSwaps(swaps)
		return "", err
	}
	return "", nil
}

func checkSum(data []byte, want string) error {
	h := sha256.Sum256(data)
	if want == "" || !strings.EqualFold(hex.EncodeToString(h[:]), strings.TrimSpace(want)) {
		return ErrChecksum
	}
	return nil
}

// swapFile writes path.new, then path → path.prev (old backup removed), path.new → path.
// Renaming a running executable is allowed on Windows too, which is the trick
// that lets us replace agentd.exe while it runs.
func swapFile(path string, data []byte) (Swap, error) {
	sw := Swap{Path: path, Prev: path + ".prev"}
	mode := os.FileMode(0o755)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm() | 0o100
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return sw, err
	}
	_ = os.RemoveAll(sw.Prev)
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, sw.Prev); err != nil {
			os.Remove(tmp)
			return sw, err
		}
	} else {
		sw.Prev = ""
	}
	if err := os.Rename(tmp, path); err != nil {
		if sw.Prev != "" {
			_ = os.Rename(sw.Prev, path)
		}
		return sw, err
	}
	return sw, nil
}

// swapAppBundle extracts a tar.gz holding "<name>.app/..." next to the bundle and swaps dirs.
func swapAppBundle(app string, data []byte) (Swap, error) {
	sw := Swap{Path: app, Prev: app + ".prev"}
	stage := app + ".new"
	_ = os.RemoveAll(stage)
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return sw, err
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			os.RemoveAll(stage)
			return sw, err
		}
		name := filepath.ToSlash(h.Name)
		i := strings.Index(name, ".app/")
		if i < 0 {
			continue
		}
		rel := name[i+5:]
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		dst := filepath.Join(stage, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(dst, 0o755)
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777|0o600)
			if err != nil {
				os.RemoveAll(stage)
				return sw, err
			}
			_, err = io.Copy(f, io.LimitReader(tr, 400<<20))
			f.Close()
			if err != nil {
				os.RemoveAll(stage)
				return sw, err
			}
			found = true
		case tar.TypeSymlink:
			if !strings.HasPrefix(h.Linkname, "/") && !strings.Contains(h.Linkname, "..") {
				_ = os.MkdirAll(filepath.Dir(dst), 0o755)
				_ = os.Symlink(h.Linkname, dst)
			}
		}
	}
	if !found {
		os.RemoveAll(stage)
		return sw, errors.New("archive has no .app bundle")
	}
	_ = os.RemoveAll(sw.Prev)
	if err := os.Rename(app, sw.Prev); err != nil {
		os.RemoveAll(stage)
		return sw, err
	}
	if err := os.Rename(stage, app); err != nil {
		_ = os.Rename(sw.Prev, app)
		return sw, err
	}
	return sw, nil
}

func rollbackSwaps(swaps []Swap) error {
	var first error
	for i := len(swaps) - 1; i >= 0; i-- {
		s := swaps[i]
		if s.Prev == "" {
			continue
		}
		bad := s.Path + ".failed"
		_ = os.RemoveAll(bad)
		_ = os.Rename(s.Path, bad)
		if err := os.Rename(s.Prev, s.Path); err != nil && first == nil {
			first = err
		}
		_ = os.RemoveAll(bad)
	}
	return first
}

// BootState is what a starting process should do about a pending update.
type BootState struct {
	Pending    *Pending
	RolledBack bool // the previous build is back in place; re-exec it
}

// Boot is called early at startup. It counts start attempts of a new build and
// rolls back once a build has failed to confirm MaxAttempts times.
func Boot(stateDir string) BootState {
	var p Pending
	if readJSON(pendingPath(stateDir), &p) != nil {
		return BootState{}
	}
	p.Attempts++
	if p.Attempts > MaxAttempts {
		err := rollbackSwaps(p.Swaps)
		r := Result{From: p.From, To: p.To, Status: "rolled_back", Detail: "new version did not start", At: time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			r.Status, r.Detail = "failed", "rollback failed: "+err.Error()
		}
		_ = writeJSON(resultPath(stateDir), r)
		_ = os.Remove(pendingPath(stateDir))
		return BootState{RolledBack: err == nil}
	}
	_ = writeJSON(pendingPath(stateDir), p)
	return BootState{Pending: &p}
}

// Confirm marks the running build healthy and drops the marker (backup stays).
func Confirm(stateDir string) *Result {
	var p Pending
	if readJSON(pendingPath(stateDir), &p) != nil {
		return nil
	}
	r := Result{From: p.From, To: p.To, Status: "ok", At: time.Now().UTC().Format(time.RFC3339)}
	_ = writeJSON(resultPath(stateDir), r)
	_ = os.Remove(pendingPath(stateDir))
	return &r
}

// RollbackNow restores the backup immediately (health check failed in-process).
func RollbackNow(stateDir, why string) error {
	var p Pending
	if err := readJSON(pendingPath(stateDir), &p); err != nil {
		return errors.New("no pending update")
	}
	err := rollbackSwaps(p.Swaps)
	r := Result{From: p.From, To: p.To, Status: "rolled_back", Detail: why, At: time.Now().UTC().Format(time.RFC3339)}
	if err != nil {
		r.Status, r.Detail = "failed", "rollback failed: "+err.Error()
	}
	_ = writeJSON(resultPath(stateDir), r)
	_ = os.Remove(pendingPath(stateDir))
	return err
}

// LastResult returns the last finished update, if any.
func LastResult(stateDir string) *Result {
	var r Result
	if readJSON(resultPath(stateDir), &r) != nil {
		return nil
	}
	return &r
}

// Relaunch starts exe with args detached (same env) so the caller can exit.
// Under systemd (INVOCATION_ID set) the caller should instead exit non-zero
// and let Restart=on-failure bring the new binary up.
func Relaunch(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	return cmd.Start()
}

// UnderServiceManager reports whether a supervisor will restart us on exit.
func UnderServiceManager() bool {
	return os.Getenv("INVOCATION_ID") != "" || os.Getenv("TIDEBOT_SUPERVISED") == "1"
}

func extractServer(kind string, data []byte) (map[string][]byte, error) {
	want := map[string]bool{"agentd": true, "agentctl": true, "agentd.exe": true, "agentctl.exe": true}
	out := map[string][]byte{}
	if kind == "desktop-zip" {
		want["tidebot-desktop.exe"] = true
	}
	if kind == "server-zip" || kind == "desktop-zip" {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if b := filepath.Base(f.Name); want[b] {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				body, err := io.ReadAll(io.LimitReader(rc, 200<<20))
				rc.Close()
				if err != nil {
					return nil, err
				}
				out[b] = body
			}
		}
		return out, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if b := filepath.Base(h.Name); want[b] && h.Typeflag == tar.TypeReg {
			body, err := io.ReadAll(io.LimitReader(tr, 200<<20))
			if err != nil {
				return nil, err
			}
			out[b] = body
		}
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Handover starts the freshly installed exe and waits until healthy() reports
// true. If it exits or stays unhealthy past timeout, the new process is killed,
// the backup restored, and the previous build started instead. Used when no
// service manager will restart us.
func Handover(exe string, args []string, stateDir string, healthy func() bool, timeout time.Duration) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	detach(cmd)
	if err := cmd.Start(); err != nil {
		_ = RollbackNow(stateDir, "start: "+err.Error())
		return Relaunch(exe, args)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			deadline = time.Now() // fall through to rollback
			continue
		case <-time.After(time.Second):
		}
		if healthy() {
			_ = cmd.Process.Release()
			return nil
		}
	}
	_ = cmd.Process.Kill()
	if err := RollbackNow(stateDir, "new version did not become healthy"); err != nil {
		return err
	}
	return Relaunch(exe, args)
}

// IsPending reports whether an update is still waiting for confirmation.
func IsPending(stateDir string) bool {
	_, err := os.Stat(pendingPath(stateDir))
	return err == nil
}

// startExe is our path as of process start. os.Executable follows the inode on
// Linux, so after the swap it would point at the .prev backup.
var startExe = func() string {
	p, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}()

// Executable returns the install path of the running program (stable across swaps).
func Executable() string { return startExe }
