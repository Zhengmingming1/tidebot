// Package claudebin finds the Claude Code CLI the way a terminal would.
//
// GUI apps on macOS (and some Linux launchers) start with a bare PATH
// (/usr/bin:/bin:/usr/sbin:/sbin), so `claude` installed via npm, nvm, Homebrew
// or the native installer is invisible. We resolve it in this order, like
// opcode / cc-switch / claudecodeui do:
//
//  1. a path the user set (config claude_bin, if it isn't just "claude")
//  2. the user's login shell: `$SHELL -ilc 'command -v claude; echo $PATH'`
//  3. the current PATH
//  4. well-known install dirs (native installer, npm/pnpm/yarn/bun/volta,
//     Homebrew, nvm/fnm/asdf/mise node versions newest first, Windows npm)
//
// Every candidate is verified by running `<candidate> --version`. The login
// shell PATH (plus the directory claude lives in, so an npm shebang finds
// node) is returned for use as the child process PATH.
package claudebin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result describes where claude was found (or everywhere we looked).
type Result struct {
	Path     string    `json:"path,omitempty"`
	Version  string    `json:"version,omitempty"`
	Method   string    `json:"method"` // configured | login_shell | path | known_dir | not_found
	Searched []string  `json:"searched"`
	PATH     string    `json:"-"` // PATH to give claude's child process
	Note     string    `json:"note,omitempty"`
	At       time.Time `json:"-"`
}

// Options control resolution. Zero values use the real environment.
type Options struct {
	Configured string // config claude_bin; "" or "claude" = auto
	Home       string // defaults to os.UserHomeDir
	Env        func(string) string
	GOOS       string
	Shell      string // defaults to $SHELL (unix)
	Timeout    time.Duration
	// Version runs `bin --version` with PATH=path; tests replace it.
	Version func(ctx context.Context, bin, path string) (string, error)
}

func (o *Options) fill() {
	if o.Env == nil {
		o.Env = os.Getenv
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.Shell == "" {
		o.Shell = o.Env("SHELL")
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Version == nil {
		o.Version = runVersion
	}
}

func listSep(goos string) string {
	if goos == "windows" {
		return ";"
	}
	return ":"
}

// Resolve finds claude. It never returns an error; check Result.Path.
func Resolve(ctx context.Context, o Options) Result {
	o.fill()
	sep := listSep(o.GOOS)
	r := Result{Method: "not_found", Searched: []string{}, At: time.Now()}
	basePATH := o.Env("PATH")
	shellPATH := ""
	seen := map[string]bool{}
	try := func(bin, method, path string) bool {
		if bin == "" || seen[bin] {
			return false
		}
		seen[bin] = true
		r.Searched = append(r.Searched, bin)
		if !isExec(bin, o.GOOS) {
			return false
		}
		childPATH := joinPATH(sep, filepath.Dir(bin), path, basePATH)
		vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		v, err := o.Version(vctx, bin, childPATH)
		if err != nil || v == "" {
			return false
		}
		r.Path, r.Version, r.Method, r.PATH = bin, v, method, childPATH
		return true
	}

	// 1. explicit path
	if c := strings.TrimSpace(o.Configured); c != "" && c != "claude" {
		c = expandHome(c, o.Home)
		if !strings.ContainsAny(c, `/\`) {
			if lp, err := exec.LookPath(c); err == nil {
				c, _ = filepath.Abs(lp)
			}
		}
		if try(c, "configured", "") {
			return r
		}
		r.Note = "configured path did not run"
		return r // an explicit choice is never silently replaced
	}
	// 2. login shell
	if o.GOOS != "windows" && o.Shell != "" {
		bin, p := loginShell(ctx, o.Shell, o.Timeout)
		shellPATH = p
		if bin != "" && filepath.IsAbs(bin) && try(bin, "login_shell", p) {
			return r
		}
		r.Searched = append(r.Searched, o.Shell+" -ilc 'command -v claude'")
	}
	// 3. PATH (current + login shell)
	names := []string{"claude"}
	if o.GOOS == "windows" {
		names = []string{"claude.exe", "claude.cmd", "claude"}
	}
	for _, dir := range splitPATH(joinPATH(sep, shellPATH, basePATH), sep) {
		for _, n := range names {
			if try(filepath.Join(dir, n), "path", shellPATH) {
				return r
			}
		}
	}
	// 4. known dirs
	for _, dir := range KnownDirs(o.Home, o.GOOS, o.Env) {
		for _, n := range names {
			if try(filepath.Join(dir, n), "known_dir", shellPATH) {
				return r
			}
		}
	}
	if o.GOOS == "windows" {
		r.Note = "If Claude Code is installed inside WSL, install it on Windows too (npm i -g @anthropic-ai/claude-code) or set its path."
	}
	return r
}

// KnownDirs lists install locations to probe, most specific first.
func KnownDirs(home, goos string, env func(string) string) []string {
	h := func(p ...string) string { return filepath.Join(append([]string{home}, p...)...) }
	var d []string
	if goos == "windows" {
		appdata, local := env("APPDATA"), env("LOCALAPPDATA")
		if appdata == "" {
			appdata = h("AppData", "Roaming")
		}
		if local == "" {
			local = h("AppData", "Local")
		}
		d = append(d, h(".claude", "local"), h(".local", "bin"), filepath.Join(appdata, "npm"),
			filepath.Join(local, "Programs", "claude"), filepath.Join(local, "Programs", "Claude"),
			filepath.Join(local, "pnpm"), h(".bun", "bin"), filepath.Join(local, "Volta", "bin"), h(".volta", "bin"),
			filepath.Join(local, "Yarn", "bin"))
		if nv := env("NVM_SYMLINK"); nv != "" {
			d = append(d, nv)
		}
		return d
	}
	if v := env("NVM_BIN"); v != "" {
		d = append(d, v)
	}
	d = append(d, h(".claude", "local"), h(".claude", "bin"), h(".local", "bin"), h(".npm-global", "bin"), h(".npm", "bin"),
		"/opt/homebrew/bin", "/usr/local/bin", h(".bun", "bin"), h(".volta", "bin"),
		h(".local", "share", "pnpm"), h("Library", "pnpm"), h(".yarn", "bin"), h(".config", "yarn", "global", "node_modules", ".bin"))
	if p := env("NPM_CONFIG_PREFIX"); p != "" {
		d = append(d, filepath.Join(p, "bin"))
	}
	if p := env("PNPM_HOME"); p != "" {
		d = append(d, p)
	}
	d = append(d, nodeVersionBins(h(".nvm", "versions", "node"))...)
	d = append(d, nodeVersionBins(h(".local", "share", "fnm", "node-versions"), "installation")...)
	d = append(d, nodeVersionBins(h("Library", "Application Support", "fnm", "node-versions"), "installation")...)
	d = append(d, nodeVersionBins(h(".asdf", "installs", "nodejs"))...)
	d = append(d, nodeVersionBins(h(".local", "share", "mise", "installs", "node"))...)
	d = append(d, "/usr/bin", "/snap/bin")
	return d
}

// nodeVersionBins returns <root>/<version>[/sub]/bin, newest version first.
func nodeVersionBins(root string, sub ...string) []string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var vs []string
	for _, e := range ents {
		if e.IsDir() {
			vs = append(vs, e.Name())
		}
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[j], vs[i]) })
	var out []string
	for _, v := range vs {
		out = append(out, filepath.Join(append(append([]string{root, v}, sub...), "bin")...))
	}
	return out
}

func versionLess(a, b string) bool {
	pa, pb := nums(a), nums(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x < y
		}
	}
	return a < b
}

func nums(s string) []int {
	s = strings.TrimPrefix(s, "v")
	var out []int
	for _, p := range strings.Split(s, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

const marker = "__TIDEBOT_PATH__"

// loginShell asks the user's shell (interactive login, so .zshrc/.bashrc,
// nvm and Homebrew shellenv run) where claude is and what PATH is.
func loginShell(ctx context.Context, shell string, timeout time.Duration) (bin, path string) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	script := "command -v claude 2>/dev/null; printf '\\n" + marker + "%s\\n' \"$PATH\""
	cmd := exec.CommandContext(ctx, shell, "-ilc", script)
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "TERM=dumb")
	var out bytes.Buffer
	cmd.Stdout = &out
	setNoTTY(cmd)
	_ = cmd.Run()
	return parseShellOutput(out.String())
}

func parseShellOutput(s string) (bin, path string) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, marker) {
			path = strings.TrimPrefix(line, marker)
		} else if strings.HasPrefix(line, "/") && strings.HasSuffix(line, "/claude") {
			bin = line // last absolute hit wins over rc-file noise
		}
	}
	return
}

func runVersion(ctx context.Context, bin, path string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = WithPATH(os.Environ(), path)
	setNoTTY(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	v := ParseVersion(string(out))
	if v == "" {
		return "", errors.New("no version in output")
	}
	return v, nil
}

// ParseVersion extracts "2.1.3" from "2.1.3 (Claude Code)".
func ParseVersion(s string) string {
	for _, f := range strings.Fields(s) {
		if len(f) > 0 && f[0] >= '0' && f[0] <= '9' && strings.Count(f, ".") >= 1 {
			return strings.TrimRight(f, ",")
		}
	}
	return ""
}

// WithPATH returns env with PATH replaced (no-op for empty path).
func WithPATH(env []string, path string) []string {
	if path == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+path)
}

func joinPATH(sep string, parts ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		for _, d := range splitPATH(p, sep) {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return strings.Join(out, sep)
}

func splitPATH(p, sep string) []string {
	var out []string
	for _, d := range strings.Split(p, sep) {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func isExec(p, goos string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
}

// Cache memoizes one Result; Get(force) re-resolves.
type Cache struct {
	mu  sync.Mutex
	r   *Result
	TTL time.Duration
}

func (c *Cache) Get(ctx context.Context, o Options, force bool) Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.TTL
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	if !force && c.r != nil && time.Since(c.r.At) < ttl && c.r.Path != "" {
		return *c.r
	}
	r := Resolve(ctx, o)
	c.r = &r
	return r
}

// Invalidate drops the cached result.
func (c *Cache) Invalidate() { c.mu.Lock(); c.r = nil; c.mu.Unlock() }
