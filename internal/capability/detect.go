package capability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DetectOptions struct {
	OnlyConfigured bool              // Do not fall back to PATH when a reviewed dependency is absent.
	Paths          map[string]string // Explicit, user-configured executable paths.
	PATH           string            // Empty uses the process PATH.
	ExtraDirs      []string
	Timeout        time.Duration // At most five seconds per command.
}

var executableName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)
var versionNumber = regexp.MustCompile(`\b([0-9]+)\.([0-9]+)(?:\.([0-9]+))?\b`)

// Detect invokes only fixed version flags, never an upstream check program,
// authentication command, shell expression, interpreter -c, or package manager.
// Login-shell startup is deliberately not run: it can execute arbitrary rc code.
func Detect(ctx context.Context, requirements []ToolRequirement, options DetectOptions) map[string]Resolved {
	out := map[string]Resolved{}
	if len(requirements) > 64 {
		requirements = requirements[:64]
	}
	for _, r := range requirements {
		out[r.Name] = detectOne(ctx, r, options)
	}
	return out
}
func detectOne(ctx context.Context, r ToolRequirement, o DetectOptions) Resolved {
	result := Resolved{Source: "system", Status: "missing"}
	if !executableName.MatchString(r.Name) {
		result.Status = "unsupported"
		result.Note = "Dependency must be a command name"
		return result
	}
	args := r.Check
	if len(args) == 0 {
		args = []string{"--version"}
	}
	if len(args) != 1 || (args[0] != "--version" && args[0] != "-V" && args[0] != "-version") {
		result.Status = "unsupported"
		result.Note = "Only fixed version checks are permitted"
		return result
	}
	// A version argument does not make general-purpose launchers safe: npx can
	// install a package before forwarding flags, and shells can load startup code.
	switch strings.ToLower(r.Name) {
	case "npx", "uvx", "sh", "bash", "zsh", "fish", "cmd", "powershell", "pwsh":
		result.Status = "unsupported"
		result.Note = "Launcher checks are not supported"
		return result
	}
	p := o.Paths[r.Name]
	if p != "" {
		if !filepath.IsAbs(p) {
			result.Status = "unsupported"
			result.Note = "Configured tool path must be absolute"
			return result
		}
	} else {
		if o.OnlyConfigured {
			return result
		}
		search := o.PATH
		if search == "" {
			search = os.Getenv("PATH")
		}
		home, _ := os.UserHomeDir()
		dirs := append(filepath.SplitList(search), loginPathHints(home, os.Getenv("SHELL"), search)...)
		dirs = append(dirs, o.ExtraDirs...)
		dirs = append(dirs, commonToolDirectories(home)...)
		for _, dir := range dirs {
			if !filepath.IsAbs(dir) {
				continue
			}
			candidate := filepath.Join(dir, r.Name)
			candidates := []string{candidate}
			if runtime.GOOS == "windows" && filepath.Ext(candidate) == "" {
				candidates = []string{candidate + ".exe", candidate + ".com"}
				// Only the known npm shim is supported; never execute arbitrary
				// PATHEXT scripts or shells discovered under another name.
				if strings.EqualFold(r.Name, "npm") {
					candidates = append(candidates, candidate+".cmd")
				}
			}
			for _, candidate := range candidates {
				if executable(candidate) {
					p = candidate
					break
				}
			}
			if p != "" {
				break
			}
		}
	}
	if p == "" || !executable(p) {
		return result
	}
	result.Path = p
	timeout := o.Timeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 3 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, p, args...)
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(p), ".cmd") {
		root := os.Getenv("SystemRoot")
		if !strings.EqualFold(r.Name, "npm") || !filepath.IsAbs(root) || strings.ContainsAny(p, "\"&|<>^%!\r\n") {
			result.Status = "unsupported"
			result.Note = "Unsafe Windows command shim"
			return result
		}
		cmd = exec.CommandContext(checkCtx, filepath.Join(root, "System32", "cmd.exe"), "/d", "/s", "/c", "\"\""+p+"\" "+args[0]+"\"")
	}
	afterStart, cleanup, processErr := configureCheckProcess(cmd)
	if processErr != nil {
		result.Status = "error"
		result.Note = "Version process ownership failed"
		return result
	}
	defer cleanup()
	cmd.Stdin = nil
	cmd.WaitDelay = 200 * time.Millisecond
	output := &boundedOutput{limit: 16 << 10}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Start()
	if err == nil {
		err = afterStart()
		if err != nil {
			_ = cmd.Process.Kill()
		}
		waitErr := cmd.Wait()
		if err == nil {
			err = waitErr
		}
	}
	if err != nil {
		result.Status = "error"
		result.Note = "Version check failed"
		if checkCtx.Err() != nil {
			result.Note = "Version check timed out or cancelled"
		}
		return result
	}
	ver := versionNumber.FindString(output.String())
	result.Version = ver
	if ver == "" {
		result.Status = "unknown"
		result.Note = "Could not identify version"
		return result
	}
	ok, err := SatisfiesVersion(ver, r.Version)
	if err != nil {
		result.Status = "unsupported"
		result.Note = err.Error()
	} else if !ok {
		result.Status = "incompatible"
		result.Note = "Installed version does not satisfy requirement"
	} else {
		result.Status = "ready"
	}
	return result
}
func executable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0)
}

type boundedOutput struct {
	mu    sync.Mutex
	b     []byte
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - len(b.b)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.b = append(b.b, p...)
	}
	return n, nil
}
func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.b) }

var _ io.Writer = (*boundedOutput)(nil)

// SatisfiesVersion supports exact and simple numeric bounds only. Unsupported
// semver syntax fails closed rather than silently treating a dependency as ready.
func SatisfiesVersion(actual, constraint string) (bool, error) {
	if strings.TrimSpace(constraint) == "" {
		return true, nil
	}
	constraint = strings.TrimSpace(constraint)
	op := "="
	for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(constraint, prefix) {
			op = prefix
			constraint = strings.TrimSpace(strings.TrimPrefix(constraint, prefix))
			break
		}
	}
	if op == "^" || op == "~" {
		return false, errors.New("unsupported version constraint")
	}
	parse := func(s string) ([3]int, error) {
		var v [3]int
		s = strings.TrimPrefix(s, "v")
		m := versionNumber.FindStringSubmatch(s)
		if len(m) == 0 || m[0] != s {
			return v, fmt.Errorf("unsupported version %q", s)
		}
		for i := 0; i < 3; i++ {
			if m[i+1] != "" {
				n, err := strconv.Atoi(m[i+1])
				if err != nil {
					return v, err
				}
				v[i] = n
			}
		}
		return v, nil
	}
	a, err := parse(actual)
	if err != nil {
		return false, err
	}
	b, err := parse(constraint)
	if err != nil {
		return false, err
	}
	cmp := 0
	for i := range a {
		if a[i] < b[i] {
			cmp = -1
			break
		}
		if a[i] > b[i] {
			cmp = 1
			break
		}
	}
	switch op {
	case "=":
		return cmp == 0, nil
	case ">=":
		return cmp >= 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case "<":
		return cmp < 0, nil
	}
	return false, errors.New("unsupported version constraint")
}
