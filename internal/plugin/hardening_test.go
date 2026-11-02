package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type bufferCloser struct{ bytes.Buffer }

func (b *bufferCloser) Close() error { return nil }

func TestHostKVAndLogRequireGrants(t *testing.T) {
	s := newStore(t)
	var logs bytes.Buffer
	h := NewHost(s, &fakeAPI{}, slog.New(slog.NewTextHandler(&logs, nil)))
	for _, method := range []string{"kv.get", "kv.set", "log.write"} {
		var out bufferCloser
		p := &proc{pluginID: "demo-plugin", stdin: &out}
		id := int64(1)
		h.handle(p, &Manifest{}, rpcMsg{ID: &id, Method: method, Params: json.RawMessage(`{"key":"x","value":"x","message":"private-fixture"}`)})
		var response rpcMsg
		if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.Error == nil || !strings.Contains(response.Error.Message, "permission denied") {
			t.Fatalf("%s: %s %v", method, out.String(), err)
		}
	}
	if logs.Len() != 0 {
		t.Fatal("denied log was written")
	}
	var out bufferCloser
	p := &proc{pluginID: "demo-plugin", stdin: &out, secrets: []string{"fixture-value"}}
	id := int64(1)
	h.handle(p, &Manifest{Permissions: Permissions{HostAPI: []string{"log.write"}}}, rpcMsg{ID: &id, Method: "log.write", Params: json.RawMessage(`{"message":"token=fixture-value","level":"fixture-value"}`)})
	if strings.Contains(logs.String(), "fixture-value") || !strings.Contains(logs.String(), "***") {
		t.Fatal("secret not redacted", logs.String())
	}
}

func TestMCPRevokedSecretIsNotInjected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	dir := writePlugin(t, "1.0.0", "kv", false)
	path := filepath.Join(dir, "plugin.yaml")
	raw, _ := os.ReadFile(path)
	raw = append(raw, []byte("contributes:\n  connectors:\n    - id: demo\n      mcp:\n        command: tool\n        env:\n          TOKEN: '${secret:token}'\n")...)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	v, err := s.Stage(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Approve(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSecret(ctx, "demo-plugin", "token", "fixture-private-token"); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.MCPConfig(ctx, false)
	if err != nil || !strings.Contains(string(before), "fixture-private-token") {
		t.Fatalf("%s %v", before, err)
	}
	if err = s.SetPermission(ctx, "demo-plugin", "secret:token", false); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.MCPConfig(ctx, false)
	if err != nil || strings.Contains(string(after), "fixture-private-token") {
		t.Fatalf("revocation ignored: %s %v", after, err)
	}
}

func TestConcurrentLazyStartReturnsOneProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("existing runnable fixture lacks .exe")
	}
	t.Setenv("TIDEBOT_TEST_PLUGIN", "1")
	s := newStore(t)
	installRunnable(t, s, "kv")
	h := NewHost(s, &fakeAPI{}, nil)
	defer h.StopAll()
	var wg sync.WaitGroup
	results := make(chan *proc, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := h.ensure("demo-plugin")
			if err != nil {
				t.Error(err)
			}
			results <- p
		}()
	}
	wg.Wait()
	close(results)
	var first *proc
	for p := range results {
		if first == nil {
			first = p
		}
		if p != first {
			t.Fatal("duplicate plugin processes launched")
		}
	}
}
