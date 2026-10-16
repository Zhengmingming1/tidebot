package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Check is one self-check result.
type Check struct {
	Name   string `json:"name"` // manifest | files | schema | start | handshake | enumerate | network
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// SelfCheckLimits bound what an (AI-)generated plugin may contain.
const (
	MaxPluginFiles = 60
	MaxPluginBytes = 2 << 20
)

var reLooksSecret = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{16,}|ghp_[a-z0-9]{20,}|xox[abp]-[a-z0-9-]{10,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

// SelfCheck validates a plugin directory and, if it has a runtime, starts it in isolation:
// plugin.activate handshake, then every connector's MCP initialize + tools/list.
// Nothing is installed. Secrets are never passed; the process gets an empty HOME.
func SelfCheck(ctx context.Context, dir string) (checks []Check, m *Manifest) {
	add := func(name string, err error, okDetail string) bool {
		c := Check{Name: name, OK: err == nil, Detail: okDetail}
		if err != nil {
			c.Detail = err.Error()
		}
		checks = append(checks, c)
		return err == nil
	}
	m, err := ParseManifest(dir)
	if !add("manifest", err, "plugin.yaml OK") {
		return checks, nil
	}
	if m.UsesBuiltin() {
		add("manifest", errors.New("builtin runtimes are reserved for official plugins"), "")
		return checks, m
	}
	n, size, err := scanFiles(dir)
	add("files", err, fmt.Sprintf("%d files, %d KB", n, size/1024))
	var serr error
	nfields := 0
	if m.ConfigSchema != nil {
		f, e := ParseSchema(m.ConfigSchema)
		serr, nfields = e, len(f)
	}
	for _, t := range m.Contributes.Triggers {
		if serr == nil && t.ConfigSchema != nil {
			_, serr = ParseSchema(t.ConfigSchema)
		}
	}
	add("schema", serr, fmt.Sprintf("%d settings fields", nfields))
	if m.Runtime.Command == "" && len(m.Contributes.Connectors) == 0 {
		return checks, m
	}
	isolated := len(m.Permissions.Network) == 0 && canUnshareNet()
	if len(m.Permissions.Network) == 0 {
		if isolated {
			add("network", nil, "started without network")
		} else {
			checks = append(checks, Check{Name: "network", OK: true, Detail: "network isolation unavailable on this system"})
		}
	}
	home, _ := os.MkdirTemp("", "plugin-check-home-")
	defer os.RemoveAll(home)
	if m.Runtime.Command != "" {
		makeExec(dir, m.Runtime.Command)
		cfg := defaults(m.ConfigSchema)
		res, err := rpcProbe(ctx, dir, home, isolated, m.Runtime.Command, m.Runtime.Args, []probeCall{
			{"plugin.activate", map[string]any{"config": cfg, "data_dir": home}},
		})
		if add("start", startErr(err), "process started") {
			add("handshake", err, "plugin.activate answered"+short(res))
		}
		if len(m.Contributes.Triggers) > 0 {
			var ids []string
			for _, t := range m.Contributes.Triggers {
				ids = append(ids, t.Kind+":"+t.ID)
			}
			checks = append(checks, Check{Name: "enumerate", OK: true, Detail: "triggers " + strings.Join(ids, ", ")})
		}
	}
	for _, c := range m.Contributes.Connectors {
		makeExec(dir, c.MCP.Command)
		res, err := rpcProbe(ctx, dir, home, isolated, c.MCP.Command, c.MCP.Args, []probeCall{
			{"initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "tidebot-selfcheck", "version": "1"}}},
			{"tools/list", map[string]any{}},
		})
		if !add("start", startErr(err), "connector "+c.ID+" started") {
			continue
		}
		var tl struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err == nil {
			_ = json.Unmarshal(res, &tl)
			if len(tl.Tools) == 0 {
				err = errors.New("tools/list returned no tools")
			}
		}
		var names []string
		for _, t := range tl.Tools {
			names = append(names, t.Name)
		}
		add("enumerate", err, "connector "+c.ID+" tools: "+strings.Join(names, ", "))
	}
	return checks, m
}

// Passed reports whether every check is OK.
func Passed(cs []Check) bool {
	for _, c := range cs {
		if !c.OK {
			return false
		}
	}
	return len(cs) > 0
}

type startError struct{ error }

func startErr(err error) error {
	var s startError
	if errors.As(err, &s) {
		return s.error
	}
	return nil
}

func short(b json.RawMessage) string {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" || s == "{}" {
		return ""
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return ": " + s
}

func defaults(sc map[string]any) map[string]any {
	out := map[string]any{}
	if f, err := ParseSchema(sc); err == nil {
		for _, x := range f {
			if x.Default != nil && !x.WriteOnly {
				out[x.Key] = x.Default
			}
		}
	}
	return out
}

func makeExec(dir, rel string) {
	if rel == "" || insideDir(dir, rel) != nil {
		return
	}
	p := filepath.Join(dir, rel)
	if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
		_ = os.Chmod(p, fi.Mode().Perm()|0o100)
	}
}

func scanFiles(dir string) (n int, size int64, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed: %s", d.Name())
		}
		if !d.Type().IsRegular() {
			return nil
		}
		n++
		fi, _ := d.Info()
		size += fi.Size()
		if n > MaxPluginFiles || size > MaxPluginBytes {
			return fmt.Errorf("plugin too large (max %d files, %d MB)", MaxPluginFiles, MaxPluginBytes>>20)
		}
		if fi.Size() < 256<<10 {
			if b, err := os.ReadFile(p); err == nil && reLooksSecret.Match(b) {
				rel, _ := filepath.Rel(dir, p)
				return fmt.Errorf("%s looks like it contains a credential", rel)
			}
		}
		return nil
	})
	return
}

func canUnshareNet() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		return false
	}
	return exec.Command("unshare", "-rn", "true").Run() == nil
}

type probeCall struct {
	method string
	params any
}

// rpcProbe starts cmd, sends the calls in order and returns the last result.
func rpcProbe(ctx context.Context, dir, home string, isolate bool, command string, args []string, calls []probeCall) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	bin := filepath.Join(dir, command)
	var cmd *exec.Cmd
	if isolate {
		cmd = exec.CommandContext(ctx, "unshare", append([]string{"-rn", "--", bin}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, bin, args...)
	}
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TIDEBOT_PLUGIN_ID=selfcheck", "TIDEBOT_PLUGIN_DATA=" + home, "TIDEBOT_SELFCHECK=1"}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4000}
	if err := cmd.Start(); err != nil {
		return nil, startError{fmt.Errorf("cannot start %s: %v", command, err)}
	}
	defer func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	rd := bufio.NewReader(stdout)
	var last json.RawMessage
	for i, c := range calls {
		pb, _ := json.Marshal(c.params)
		id := int64(i + 1)
		line, _ := json.Marshal(rpcMsg{JSONRPC: "2.0", ID: &id, Method: c.method, Params: pb})
		if _, err := stdin.Write(append(line, '\n')); err != nil {
			return nil, fmt.Errorf("%s: process closed stdin (%s)", c.method, tail(stderr.String()))
		}
		for {
			type res struct {
				m   rpcMsg
				err error
			}
			ch := make(chan res, 1)
			go func() {
				b, err := rd.ReadBytes('\n')
				var m rpcMsg
				if err == nil {
					err = json.Unmarshal(b, &m)
					if err != nil {
						err = fmt.Errorf("non-JSON output on stdout: %.80q", string(b))
					}
				}
				ch <- res{m, err}
			}()
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%s: no answer within 20 s", c.method)
			case r := <-ch:
				if r.err != nil {
					if r.err == io.EOF {
						return nil, fmt.Errorf("%s: process exited (%s)", c.method, tail(stderr.String()))
					}
					return nil, fmt.Errorf("%s: %v", c.method, r.err)
				}
				if r.m.Method != "" { // plugin → host request/notification during the probe
					if r.m.ID != nil {
						resp, _ := json.Marshal(rpcMsg{JSONRPC: "2.0", ID: r.m.ID, Result: json.RawMessage(`{"ok":true}`)})
						_, _ = stdin.Write(append(resp, '\n'))
					}
					continue
				}
				if r.m.ID == nil || *r.m.ID != id {
					continue
				}
				if r.m.Error != nil {
					return nil, fmt.Errorf("%s: %s", c.method, r.m.Error.Message)
				}
				last = r.m.Result
			}
			break
		}
	}
	return last, nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "no stderr"
	}
	if len(s) > 300 {
		s = "…" + s[len(s)-300:]
	}
	return s
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

// FileView is one file of a generated plugin with its diff against the previous version.
type FileView struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // added | modified | removed | same
	Content string `json:"content,omitempty"`
	Diff    string `json:"diff,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

// DiffDirs compares prev (may be "") with next.
func DiffDirs(prev, next string) []FileView {
	read := func(dir string) map[string][]byte {
		out := map[string][]byte{}
		if dir == "" {
			return out
		}
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				rel, _ := filepath.Rel(dir, p)
				if b, err := os.ReadFile(p); err == nil && len(b) <= 512<<10 {
					out[filepath.ToSlash(rel)] = b
				}
			}
			return nil
		})
		return out
	}
	a, b := read(prev), read(next)
	names := map[string]bool{}
	for k := range a {
		names[k] = true
	}
	for k := range b {
		names[k] = true
	}
	var keys []string
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []FileView
	for _, k := range keys {
		x, inA := a[k]
		y, inB := b[k]
		fv := FileView{Path: k}
		bin := isBinary(x) || isBinary(y)
		switch {
		case !inA:
			fv.Status = "added"
		case !inB:
			fv.Status = "removed"
		case string(x) == string(y):
			fv.Status = "same"
		default:
			fv.Status = "modified"
		}
		if bin {
			fv.Binary = true
		} else {
			fv.Content = string(y)
			if fv.Status != "same" {
				fv.Diff = UnifiedDiff(string(x), string(y))
			}
		}
		out = append(out, fv)
	}
	return out
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

// UnifiedDiff is a small line diff (LCS) with 2 lines of context; fine for plugin-sized files.
func UnifiedDiff(a, b string) string {
	la, lb := splitLines(a), splitLines(b)
	if len(la)*len(lb) > 4_000_000 {
		return "(file too large to diff)"
	}
	n, m := len(la), len(lb)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if la[i] == lb[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	type op struct {
		k byte
		s string
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case la[i] == lb[j]:
			ops = append(ops, op{' ', la[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, op{'-', la[i]})
			i++
		default:
			ops = append(ops, op{'+', lb[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', la[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', lb[j]})
	}
	keep := make([]bool, len(ops))
	for k, o := range ops {
		if o.k != ' ' {
			for d := k - 2; d <= k+2; d++ {
				if d >= 0 && d < len(ops) {
					keep[d] = true
				}
			}
		}
	}
	var sb strings.Builder
	gap := false
	for k, o := range ops {
		if !keep[k] {
			gap = true
			continue
		}
		if gap && sb.Len() > 0 {
			sb.WriteString("@@\n")
		}
		gap = false
		sb.WriteByte(o.k)
		sb.WriteString(o.s)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
