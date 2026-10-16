package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

func TestMain(m *testing.M) {
	if os.Getenv("TIDEBOT_TEST_PLUGIN") == "1" {
		fakePlugin()
		return
	}
	os.Exit(m.Run())
}

// fakePlugin is a tiny JSON-RPC stdio plugin used by the host tests.
func fakePlugin() {
	sc := bufio.NewScanner(os.Stdin)
	out := os.Stdout
	var mu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		out.Write(append(b, '\n'))
		mu.Unlock()
	}
	for sc.Scan() {
		var m rpcMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Method == "" {
			continue
		}
		switch m.Method {
		case "plugin.activate", "plugin.shutdown":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]bool{"ok": true}})
		case "test.crash":
			os.Exit(3)
		case "test.secret":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]string{"tok": os.Getenv("TIDEBOT_SECRET_TOKEN")}})
		}
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return &Store{DB: d, Root: t.TempDir(), Secrets: secrets.NewMemory()}
}

func writePlugin(t *testing.T, version, hostAPI string, withBin bool) string {
	t.Helper()
	dir := t.TempDir()
	cmd := ""
	if withBin {
		os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
		self, _ := os.Executable()
		b, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "bin", "plug"), b, 0o755)
		cmd = "  command: bin/plug\n"
	}
	y := fmt.Sprintf("id: demo-plugin\nname: Demo\nversion: %s\nruntime:\n%s  lifecycle: lazy\npermissions:\n  host_api: [%s]\n  secrets: [token]\n", version, cmd, hostAPI)
	os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(y), 0o644)
	return dir
}

func TestManifestValidation(t *testing.T) {
	dir := t.TempDir()
	bad := map[string]string{
		"badid":   "id: Bad_ID\nname: x\nversion: 1.0.0\n",
		"escape":  "id: a\nname: x\nversion: 1.0.0\nruntime:\n  command: ../../bin/sh\n",
		"api":     "id: a\nname: x\nversion: 1.0.0\npermissions:\n  host_api: [shell.exec]\n",
		"unknown": "id: a\nname: x\nversion: 1.0.0\nsurprise: 1\n",
		"trigger": "id: a\nname: x\nversion: 1.0.0\ncontributes:\n  triggers: [{id: t, kind: sms}]\n",
	}
	for name, y := range bad {
		os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(y), 0o644)
		if _, err := ParseManifest(dir); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("id: ok-one\nname: x\nversion: 1.2.3\n"), 0o644)
	m, err := ParseManifest(dir)
	if err != nil || m.Runtime.Lifecycle != "lazy" || m.Runtime.Limits.MemoryMB != 256 {
		t.Fatalf("defaults: %v %+v", err, m)
	}
}

func TestStageApproveUpgradeRollback(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	v1, err := s.Stage(ctx, writePlugin(t, "1.0.0", "kv", false), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent(ctx, "demo-plugin", v1.ID); err == nil {
		t.Fatal("unapproved version became current")
	}
	if err := s.Approve(ctx, v1.ID, []string{"host_api:kv"}); err == nil {
		t.Fatal("partial approval accepted")
	}
	if err := s.Approve(ctx, v1.ID, v1.Requested); err != nil {
		t.Fatal(err)
	}
	v2, err := s.Stage(ctx, writePlugin(t, "1.1.0", "kv, notify.send", false), "")
	if err != nil {
		t.Fatal(err)
	}
	if np := NewPermissions(v1.Requested, v2.Requested); len(np) != 1 || np[0] != "host_api:notify.send" {
		t.Fatalf("escalation diff: %v", np)
	}
	in, _ := s.Get(ctx, "demo-plugin")
	if in.Current.Version != "1.0.0" {
		t.Fatal("staging switched current")
	}
	if err := s.Approve(ctx, v2.ID, v2.Requested); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrent(ctx, "demo-plugin", v1.ID); err != nil {
		t.Fatal(err)
	}
	in, _ = s.Get(ctx, "demo-plugin")
	if in.Current.Version != "1.0.0" || len(in.Versions) != 2 {
		t.Fatalf("rollback: %+v", in)
	}
	if _, err := s.Stage(ctx, writePlugin(t, "1.0.0", "kv", false), "deadbeef"); err == nil {
		t.Fatal("hash mismatch accepted")
	}
	if err := s.SetSecret(ctx, "demo-plugin", "other", "x"); err == nil {
		t.Fatal("undeclared secret accepted")
	}
	called := false
	s.OnUninstall = func(context.Context, string) { called = true }
	if err := s.Uninstall(ctx, "demo-plugin", false); err != nil || !called {
		t.Fatal("uninstall", err)
	}
	if _, err := s.Get(ctx, "demo-plugin"); err == nil {
		t.Fatal("still installed")
	}
}

type fakeAPI struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeAPI) TriggerFire(context.Context, string, string, json.RawMessage) error { return nil }
func (f *fakeAPI) RunsStart(context.Context, string, string, map[string]string) (string, error) {
	return "run1", nil
}
func (f *fakeAPI) NotifySend(context.Context, string, string, string) error { return nil }
func (f *fakeAPI) TasksRead(context.Context, string) (any, error)           { return nil, nil }
func (f *fakeAPI) PluginEvent(id, kind, detail string) {
	f.mu.Lock()
	f.events = append(f.events, kind)
	f.mu.Unlock()
}

func installRunnable(t *testing.T, s *Store, api string) {
	ctx := context.Background()
	v, err := s.Stage(ctx, writePlugin(t, "1.0.0", api, true), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
}

func TestHostPermissionsAndSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host tests exec the test binary without .exe")
	}
	t.Setenv("TIDEBOT_TEST_PLUGIN", "1")
	s := newStore(t)
	installRunnable(t, s, "kv")
	if err := s.SetSecret(context.Background(), "demo-plugin", "token", "s3cret"); err != nil {
		t.Fatal(err)
	}
	h := NewHost(s, &fakeAPI{}, nil)
	defer h.StopAll()
	res, err := h.Call("demo-plugin", "test.secret", nil)
	if err != nil || !strings.Contains(string(res), "s3cret") {
		t.Fatalf("secret env: %s %v", res, err)
	}
	// plugin -> host: kv.set allowed, notify.send denied
	p, _ := h.ensure("demo-plugin")
	m := &Manifest{Permissions: Permissions{HostAPI: []string{"kv"}}}
	r, w := io.Pipe()
	p2 := &proc{pluginID: "demo-plugin", stdin: w, pending: map[int64]chan rpcMsg{}}
	_ = p
	id1, id2 := int64(1), int64(2)
	go h.handle(p2, m, rpcMsg{ID: &id1, Method: "kv.set", Params: json.RawMessage(`{"key":"k","value":"v"}`)})
	go h.handle(p2, m, rpcMsg{ID: &id2, Method: "notify.send", Params: json.RawMessage(`{"title":"x"}`)})
	sc := bufio.NewScanner(r)
	got := map[int64]rpcMsg{}
	for len(got) < 2 && sc.Scan() {
		var msg rpcMsg
		json.Unmarshal(sc.Bytes(), &msg)
		got[*msg.ID] = msg
	}
	if got[1].Error != nil || got[2].Error == nil || !strings.Contains(got[2].Error.Message, "permission denied") {
		t.Fatalf("permission check: %+v", got)
	}
	if v, ok := s.KVGet(context.Background(), "demo-plugin", "k"); !ok || v != "v" {
		t.Fatal("kv not stored")
	}
}

func TestHostCrashBreaker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host tests exec the test binary without .exe")
	}
	t.Setenv("TIDEBOT_TEST_PLUGIN", "1")
	s := newStore(t)
	installRunnable(t, s, "kv")
	api := &fakeAPI{}
	h := NewHost(s, api, nil)
	defer h.StopAll()
	for i := 0; i < CrashLimit; i++ {
		if _, err := h.Call("demo-plugin", "test.crash", nil); err == nil {
			t.Fatal("crash call returned ok")
		}
		deadline := time.Now().Add(3 * time.Second)
		for h.Running("demo-plugin") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if !h.BreakerOpen("demo-plugin") {
		t.Fatal("breaker not open")
	}
	if _, err := h.Call("demo-plugin", "test.secret", nil); err == nil {
		t.Fatal("call allowed with open breaker")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.events) != 1 || api.events[0] != "plugin_breaker" {
		t.Fatalf("events %v", api.events)
	}
	h.ResetBreaker("demo-plugin")
	if _, err := h.Call("demo-plugin", "test.secret", nil); err != nil {
		t.Fatal(err)
	}
}
