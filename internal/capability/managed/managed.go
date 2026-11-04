// Package managed installs reviewed, pinned dependency recipes under tidebot's
// private data directory. It does not resolve mutable versions or execute
// upstream installer scripts. Callers must obtain user approval before install.
package managed

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/safepath"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const MaxDownloadBytes int64 = 256 << 20
const MaxInstalledBytes int64 = 500 << 20
const MaxArchiveFiles = 20000

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
var ErrAlreadyInstalled = errors.New("managed version already exists")

// HTTPDoer permits the application's proxy-aware HTTP transport and test fixtures.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
type Command struct {
	Path      string
	Args, Env []string
	Dir       string
}
type Executor interface {
	Run(context.Context, Command) error
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, c Command) error {
	if !filepath.IsAbs(c.Path) {
		return errors.New("executor requires a resolved absolute command path")
	}
	cmd, err := executableCommand(ctx, c.Path, c.Args)
	if err != nil {
		return err
	}
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	// Never capture command output: registries may include credentials in errors.
	cmd.WaitDelay = 2 * time.Second
	afterStart, cleanup, err := managedProcess(cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	if err = cmd.Start(); err != nil {
		return err
	}
	if err = afterStart(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("managed process ownership failed: %w", err)
	}
	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("managed dependency command failed: %w", err)
	}
	return nil
}

// Windows npm is commonly a .cmd shim. Invoke only via the system command
// processor, and reject all expansion/metacharacters rather than interpolate
// potentially executable shell text. Other executables bypass the shell.
func executableCommand(ctx context.Context, path string, args []string) (*exec.Cmd, error) {
	if runtime.GOOS != "windows" || (strings.ToLower(filepath.Ext(path)) != ".cmd" && strings.ToLower(filepath.Ext(path)) != ".bat") {
		return exec.CommandContext(ctx, path, args...), nil
	}
	all := append([]string{path}, args...)
	quoted := make([]string, len(all))
	for j, value := range all {
		if strings.ContainsAny(value, "\"&|<>^%!\r\n") {
			return nil, errors.New("unsafe Windows command argument")
		}
		quoted[j] = "\"" + value + "\""
	}
	systemRoot := os.Getenv("SystemRoot")
	if !filepath.IsAbs(systemRoot) {
		return nil, errors.New("Windows system command processor unavailable")
	}
	return exec.CommandContext(ctx, filepath.Join(systemRoot, "System32", "cmd.exe"), "/d", "/s", "/c", "\""+strings.Join(quoted, " ")+"\""), nil
}

// SignatureVerifier must authenticate the artifact against a trusted publisher,
// not merely validate a signature supplied by the artifact's own untrusted key.
type SignatureVerifier interface {
	Verify(context.Context, string, Signature) error
}
type Signature struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Identity string `json:"identity"`
}
type MalwareChecker interface {
	Check(context.Context, string, string, string) error
}
type Installer struct {
	Quotas         Quotas
	DataDir        string
	Client         HTTPDoer
	Executor       Executor
	Signatures     SignatureVerifier
	MalwareChecker MalwareChecker
	// GOOS/GOARCH are optional platform overrides for deterministic planning/tests.
	GOOS, GOARCH string
	Timeout      time.Duration
	mu           sync.Mutex
}
type Result struct {
	Source       string            `json:"source"`
	Directory    string            `json:"directory,omitempty"`
	Version      string            `json:"version"`
	BinDirs      []string          `json:"bin_dirs,omitempty"`
	Executables  map[string]string `json:"executables,omitempty"`
	Verification string            `json:"verification"`
}
type SystemResolver interface {
	ResolveCompatible(context.Context, string, string) (Result, bool, error)
}

// ResolveSystemFirst never upgrades or edits a discovered system installation.
func ResolveSystemFirst(ctx context.Context, resolver SystemResolver, name, constraint string, pinManaged bool, install func(context.Context) (Result, error)) (Result, error) {
	if !pinManaged && resolver != nil {
		r, ok, err := resolver.ResolveCompatible(ctx, name, constraint)
		if err != nil {
			return Result{}, err
		}
		if ok {
			r.Source = "system"
			return r, nil
		}
	}
	if install == nil {
		return Result{}, errors.New("no compatible system dependency or managed recipe; show official instructions")
	}
	return install(ctx)
}
func (i *Installer) platform() (string, string) {
	o, a := i.GOOS, i.GOARCH
	if o == "" {
		o = runtime.GOOS
	}
	if a == "" {
		a = runtime.GOARCH
	}
	return o, a
}
func (i *Installer) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	d := i.Timeout
	if d <= 0 || d > 10*time.Minute {
		d = 5 * time.Minute
	}
	return context.WithTimeout(ctx, d)
}
func validID(s string) bool { return idPattern.MatchString(s) && s != "." && s != ".." }
func pinned(s string) bool  { return versionPattern.MatchString(s) }

// secureDir refuses symlinked ancestors, including the data directory. The data
// directory must be private to tidebot; callers must not allow concurrent edits
// by untrusted local users.
func secureDir(p string) error {
	p, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	parent := filepath.Dir(p)
	if parent != p {
		if err = secureDir(parent); err != nil {
			return err
		}
	}
	info, err := safepath.LstatDirectory(p)
	if os.IsNotExist(err) {
		return os.Mkdir(p, 0700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed path contains a symlink or non-directory")
	}
	return nil
}
func (i *Installer) stage(kind, name, version string) (string, string, error) {
	if !filepath.IsAbs(i.DataDir) || !validID(name) || !validID(version) {
		return "", "", errors.New("invalid managed destination")
	}
	parent := filepath.Join(i.DataDir, kind, name)
	if err := secureDir(parent); err != nil {
		return "", "", err
	}
	dest := filepath.Join(parent, version)
	if _, err := os.Lstat(dest); err == nil {
		return "", "", ErrAlreadyInstalled
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	stage, err := os.MkdirTemp(parent, ".install-")
	return stage, dest, err
}
func promote(ctx context.Context, stage, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return ErrAlreadyInstalled
	}
	return os.Rename(stage, dest)
}
func safeRelative(s string) bool {
	if s == "" || strings.ContainsAny(s, "\\:\x00") || strings.HasPrefix(s, "/") {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == ".." || p == "" || (p != "." && (strings.HasSuffix(p, ".") || strings.HasSuffix(p, " "))) {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(s))
}
