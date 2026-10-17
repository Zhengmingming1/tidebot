package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
)

// Protocol: JSON-RPC 2.0, one JSON object per line on the plugin's stdin/stdout.
// stderr is captured into the host log (truncated).

// BuiltinExe is the tidebot executable that hosts official plugin runtimes
// (run as: BuiltinExe BuiltinArg <name> plugin|mcp). Empty disables builtin runtimes.
var BuiltinExe string

// BuiltinArg is the hidden sub-command that starts an official runtime.
const BuiltinArg = "__plugin"

// HostAPI is what plugins may call back into (each method is permission-checked).
type HostAPI interface {
	TriggerFire(ctx context.Context, pluginID, triggerID string, payload json.RawMessage) error
	RunsStart(ctx context.Context, pluginID, target string, vars map[string]string) (string, error)
	NotifySend(ctx context.Context, pluginID, title, body string) error
	TasksRead(ctx context.Context, pluginID string) (any, error)
	PluginEvent(pluginID, kind, detail string) // crash / breaker notifications
}

// Breaker: N crashes within Window open the circuit (suggested 3 in 5 min).
const (
	CrashLimit  = 3
	CrashWindow = 5 * time.Minute
)

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type proc struct {
	pluginID string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan rpcMsg
	lastUse  time.Time
	stopping bool
	done     chan struct{}
}

// Host runs and supervises plugin processes.
type Host struct {
	Store *Store
	API   HostAPI
	Log   *slog.Logger
	// Restart backoff base (tests shrink it).
	Backoff time.Duration

	mu      sync.Mutex
	procs   map[string]*proc
	crashes map[string][]time.Time
	open    map[string]bool // breaker open
	ctx     context.Context
}

// NewHost creates a host.
func NewHost(st *Store, api HostAPI, log *slog.Logger) *Host {
	if log == nil {
		log = slog.Default()
	}
	return &Host{Store: st, API: api, Log: log, Backoff: time.Second, procs: map[string]*proc{}, crashes: map[string][]time.Time{}, open: map[string]bool{}, ctx: context.Background()}
}

// Run starts keepalive plugins and the idle reaper until ctx ends.
func (h *Host) Run(ctx context.Context) {
	h.mu.Lock()
	h.ctx = ctx
	h.mu.Unlock()
	if list, err := h.Store.List(ctx); err == nil {
		for _, p := range list {
			if p.Enabled && p.Current != nil && p.Current.Manifest.Runtime.Lifecycle == "keepalive" && (p.Current.Manifest.Runtime.Command != "" || p.Current.Manifest.Runtime.Builtin != "") {
				if _, err := h.ensure(p.ID); err != nil {
					h.Log.Warn("plugin start", "plugin", p.ID, "error", err)
				}
			}
		}
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			h.StopAll()
			return
		case <-t.C:
			h.reapIdle()
		}
	}
}

// ResetBreaker closes the circuit after the owner fixed the plugin.
func (h *Host) ResetBreaker(pluginID string) {
	h.mu.Lock()
	delete(h.open, pluginID)
	delete(h.crashes, pluginID)
	h.mu.Unlock()
}

// BreakerOpen reports the circuit state.
func (h *Host) BreakerOpen(pluginID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open[pluginID]
}

func (h *Host) ensure(pluginID string) (*proc, error) {
	h.mu.Lock()
	if h.open[pluginID] {
		h.mu.Unlock()
		return nil, errors.New("plugin circuit breaker is open")
	}
	if p := h.procs[pluginID]; p != nil {
		h.mu.Unlock()
		return p, nil
	}
	h.mu.Unlock()
	in, err := h.Store.Get(h.ctx, pluginID)
	if err != nil || in.Current == nil || !in.Enabled {
		return nil, errors.New("plugin not installed or disabled")
	}
	// Only granted permissions count: revoked host APIs are refused, revoked secrets not passed.
	m := in.Current.Manifest.Restrict(in.Current.Approved)
	if m.Runtime.Command == "" && m.Runtime.Builtin == "" {
		return nil, errors.New("plugin has no runtime")
	}
	dataDir := filepath.Join(h.Store.Root, pluginID, "data")
	_ = os.MkdirAll(dataDir, 0o700)
	var cmd *exec.Cmd
	if m.Runtime.Builtin != "" {
		if in.Source != SourceOfficial || BuiltinExe == "" {
			return nil, errors.New("builtin runtime unavailable")
		}
		cmd = exec.Command(BuiltinExe, append([]string{BuiltinArg, m.Runtime.Builtin, "plugin"}, m.Runtime.Args...)...)
	} else {
		cmd = exec.Command(filepath.Join(in.Current.Dir, m.Runtime.Command), m.Runtime.Args...)
	}
	cmd.Dir = in.Current.Dir
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dataDir, "TIDEBOT_PLUGIN_ID=" + pluginID, "TIDEBOT_PLUGIN_DATA=" + dataDir}
	if v := os.Getenv("TIDEBOT_TEST_PLUGIN"); v != "" {
		env = append(env, "TIDEBOT_TEST_PLUGIN="+v)
	}
	for _, sname := range m.Permissions.Secrets {
		if v, err := h.Store.Secrets.Get(secretKey(pluginID, sname)); err == nil {
			env = append(env, "TIDEBOT_SECRET_"+strings.ToUpper(sname)+"="+v)
		}
	}
	cmd.Env = append(env, netproxy.Env()...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	applyLimits(cmd.Process.Pid, m.Runtime.Limits)
	p := &proc{pluginID: pluginID, cmd: cmd, stdin: stdin, pending: map[int64]chan rpcMsg{}, lastUse: time.Now(), done: make(chan struct{})}
	h.mu.Lock()
	h.procs[pluginID] = p
	h.mu.Unlock()
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			h.Log.Info("plugin stderr", "plugin", pluginID, "line", truncate(sc.Text(), 300))
		}
	}()
	go h.readLoop(p, stdout, m)
	go h.waitLoop(p)
	cfg := h.Store.ActivationConfig(h.ctx, pluginID)
	if _, err := h.call(p, "plugin.activate", map[string]any{"config": cfg, "data_dir": dataDir}, 10*time.Second); err != nil {
		h.Log.Warn("plugin activate", "plugin", pluginID, "error", err)
	}
	// Re-subscribe existing trigger bindings so polling triggers survive restarts.
	if rows, err := h.Store.DB.QueryContext(h.ctx, `SELECT id, config_json FROM trigger_bindings WHERE plugin_id=? AND enabled=1`, pluginID); err == nil {
		type sub struct{ id, cfg string }
		var subs []sub
		for rows.Next() {
			var x sub
			if rows.Scan(&x.id, &x.cfg) == nil {
				subs = append(subs, x)
			}
		}
		rows.Close()
		for _, x := range subs {
			var c map[string]any
			_ = json.Unmarshal([]byte(x.cfg), &c)
			_, _ = h.call(p, "trigger.subscribe", map[string]any{"binding_id": x.id, "trigger_id": c["trigger_id"], "config": c}, 10*time.Second)
		}
	}
	return p, nil
}

func (h *Host) pluginConfig(pluginID string) string {
	var cj string
	_ = h.Store.DB.QueryRow(`SELECT config_json FROM plugins WHERE id=?`, pluginID).Scan(&cj)
	return cj
}

func (h *Host) waitLoop(p *proc) {
	err := p.cmd.Wait()
	close(p.done)
	h.mu.Lock()
	if h.procs[p.pluginID] == p {
		delete(h.procs, p.pluginID)
	}
	p.mu.Lock()
	for _, ch := range p.pending {
		close(ch)
	}
	p.pending = map[int64]chan rpcMsg{}
	stopping := p.stopping
	p.mu.Unlock()
	if stopping {
		h.mu.Unlock()
		return
	}
	now := time.Now()
	var recent []time.Time
	for _, t := range append(h.crashes[p.pluginID], now) {
		if now.Sub(t) <= CrashWindow {
			recent = append(recent, t)
		}
	}
	h.crashes[p.pluginID] = recent
	tripped := len(recent) >= CrashLimit
	if tripped {
		h.open[p.pluginID] = true
	}
	ctx := h.ctx
	h.mu.Unlock()
	h.Log.Warn("plugin exited", "plugin", p.pluginID, "error", err, "crashes", len(recent))
	if tripped {
		if h.API != nil {
			h.API.PluginEvent(p.pluginID, "plugin_breaker", fmt.Sprintf("%d crashes in %s", len(recent), CrashWindow))
		}
		return
	}
	// keepalive plugins are restarted with exponential backoff; lazy ones on next demand
	in, gerr := h.Store.Get(ctx, p.pluginID)
	if gerr == nil && in.Current != nil && in.Current.Manifest.Runtime.Lifecycle == "keepalive" && ctx.Err() == nil {
		wait := h.Backoff << (len(recent) - 1)
		time.AfterFunc(wait, func() {
			if ctx.Err() == nil {
				_, _ = h.ensure(p.pluginID)
			}
		})
	}
}

func (h *Host) readLoop(p *proc, r io.Reader, m *Manifest) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var msg rpcMsg
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		if msg.Method != "" {
			go h.handle(p, m, msg)
			continue
		}
		if msg.ID != nil {
			p.mu.Lock()
			ch := p.pending[*msg.ID]
			delete(p.pending, *msg.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
}

func (p *proc) send(msg rpcMsg) error {
	msg.JSONRPC = "2.0"
	b, _ := json.Marshal(msg)
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.stdin.Write(append(b, '\n'))
	return err
}

func (h *Host) call(p *proc, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	pb, _ := json.Marshal(params)
	p.mu.Lock()
	p.nextID++
	idv := p.nextID
	ch := make(chan rpcMsg, 1)
	p.pending[idv] = ch
	p.lastUse = time.Now()
	p.mu.Unlock()
	if err := p.send(rpcMsg{ID: &idv, Method: method, Params: pb}); err != nil {
		return nil, err
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, errors.New("plugin exited")
		}
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	case <-time.After(timeout):
		p.mu.Lock()
		delete(p.pending, idv)
		p.mu.Unlock()
		return nil, errors.New("plugin call timed out")
	}
}

// Call invokes a method on a plugin, starting it if needed.
func (h *Host) Call(pluginID, method string, params any) (json.RawMessage, error) {
	p, err := h.ensure(pluginID)
	if err != nil {
		return nil, err
	}
	return h.call(p, method, params, 30*time.Second)
}

func allowed(m *Manifest, api string) bool {
	for _, a := range m.Permissions.HostAPI {
		if a == api {
			return true
		}
	}
	return false
}

func (h *Host) handle(p *proc, m *Manifest, msg rpcMsg) {
	ctx := h.ctx
	p.mu.Lock()
	p.lastUse = time.Now()
	p.mu.Unlock()
	var result any
	var err error
	deny := func(api string) bool {
		if !allowed(m, api) {
			err = fmt.Errorf("permission denied: host_api %s not granted", api)
			return true
		}
		return false
	}
	switch msg.Method {
	case "trigger.fire":
		var a struct {
			TriggerID string          `json:"trigger_id"`
			Payload   json.RawMessage `json:"payload"`
		}
		_ = json.Unmarshal(msg.Params, &a)
		if len(a.Payload) > 256<<10 {
			err = errors.New("payload too large")
		} else {
			err = h.API.TriggerFire(ctx, p.pluginID, a.TriggerID, a.Payload)
			result = map[string]bool{"ok": err == nil}
		}
	case "runs.start":
		if !deny("runs.start") {
			var a struct {
				Target string            `json:"target"`
				Vars   map[string]string `json:"vars"`
			}
			_ = json.Unmarshal(msg.Params, &a)
			var rid string
			rid, err = h.API.RunsStart(ctx, p.pluginID, a.Target, a.Vars)
			result = map[string]string{"run": rid}
		}
	case "notify.send":
		if !deny("notify.send") {
			var a struct{ Title, Body string }
			_ = json.Unmarshal(msg.Params, &a)
			err = h.API.NotifySend(ctx, p.pluginID, truncate(a.Title, 200), truncate(a.Body, 4000))
			result = map[string]bool{"ok": err == nil}
		}
	case "tasks.read":
		if !deny("tasks.read") {
			result, err = h.API.TasksRead(ctx, p.pluginID)
		}
	case "kv.get":
		var a struct{ Key string }
		_ = json.Unmarshal(msg.Params, &a)
		v, ok := h.Store.KVGet(ctx, p.pluginID, a.Key)
		result = map[string]any{"value": v, "found": ok}
	case "kv.set":
		var a struct{ Key, Value string }
		_ = json.Unmarshal(msg.Params, &a)
		err = h.Store.KVSet(ctx, p.pluginID, a.Key, a.Value)
		result = map[string]bool{"ok": err == nil}
	case "log.write":
		var a struct{ Level, Message string }
		_ = json.Unmarshal(msg.Params, &a)
		h.Log.Info("plugin log", "plugin", p.pluginID, "level", a.Level, "msg", truncate(a.Message, 1000))
		result = map[string]bool{"ok": true}
	default:
		err = fmt.Errorf("method not found: %s", msg.Method)
	}
	if msg.ID == nil {
		return // notification
	}
	resp := rpcMsg{ID: msg.ID}
	if err != nil {
		resp.Error = &rpcErr{Code: -32000, Message: err.Error()}
	} else {
		resp.Result, _ = json.Marshal(result)
	}
	_ = p.send(resp)
}

// Stop shuts one plugin down (plugin.shutdown, then kill after 3 s).
func (h *Host) Stop(pluginID string) {
	h.mu.Lock()
	p := h.procs[pluginID]
	h.mu.Unlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopping = true
	p.mu.Unlock()
	_, _ = h.call(p, "plugin.shutdown", nil, 2*time.Second)
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// StopAll stops every running plugin.
func (h *Host) StopAll() {
	h.mu.Lock()
	ids := make([]string, 0, len(h.procs))
	for k := range h.procs {
		ids = append(ids, k)
	}
	h.mu.Unlock()
	for _, k := range ids {
		h.Stop(k)
	}
}

// Running reports whether a plugin process is up.
func (h *Host) Running(pluginID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.procs[pluginID] != nil
}

func (h *Host) reapIdle() {
	h.mu.Lock()
	var idle []string
	for id, p := range h.procs {
		in, err := h.Store.Get(h.ctx, id)
		if err != nil || in.Current == nil {
			continue
		}
		rt := in.Current.Manifest.Runtime
		p.mu.Lock()
		if rt.Lifecycle == "lazy" && time.Since(p.lastUse) > time.Duration(rt.IdleExitSeconds)*time.Second {
			idle = append(idle, id)
		}
		p.mu.Unlock()
	}
	h.mu.Unlock()
	for _, id := range idle {
		h.Stop(id)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
