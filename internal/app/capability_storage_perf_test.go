package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// storageRuntimeFixture is a managed runtime shared by several commands
// (node/npm/npx) and referenced by several capability versions, as a Node MCP
// capability looks after a few upgrades.
func storageRuntimeFixture(t testing.TB, a *App, files, versions int) string {
	t.Helper()
	dir := filepath.Join(a.Cfg.DataDir, "runtimes", "node", "22.11.0")
	for i := 0; i < files; i++ {
		sub := filepath.Join(dir, "lib", "node_modules", fmt.Sprintf("p%03d", i/100))
		if i%100 == 0 {
			if err := os.MkdirAll(sub, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%d.js", i)), []byte("module.exports=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved := map[string]capability.Resolved{}
	for _, name := range []string{"node", "npm", "npx"} {
		exe := filepath.Join(dir, "bin", name)
		if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
		resolved[name] = capability.Resolved{Source: "managed", Directory: dir, Path: exe, Version: "22.11.0"}
	}
	if _, err := a.DB.Exec(`INSERT INTO capabilities(id,title,status,current_version_id,created_at,updated_at) VALUES('node-cap','Node MCP','installed','nv1','now','now')`); err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= versions; v++ {
		lock := capability.Lock{Format: capability.LockFormat, ID: "node-cap", Resolved: resolved}
		raw, _ := json.Marshal(lock)
		sum := sha256.Sum256(raw)
		if _, err := a.DB.Exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES(?,'node-cap',?,?,'{}',?)`,
			fmt.Sprintf("nv%d", v), string(raw), hex.EncodeToString(sum[:]), nowUTC()); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The storage view must not hold the single SQLite connection while it walks
// managed directories, and must walk each directory once.
func TestCapabilityStorageViewWalksWithoutHoldingDB(t *testing.T) {
	a := newTestApp(t)
	dir := storageRuntimeFixture(t, a, 50, 4)
	var mu sync.Mutex
	walks := map[string]int{}
	orig := capabilityStorageWalk
	t.Cleanup(func() { capabilityStorageWalk = orig })
	capabilityStorageWalk = func(ctx context.Context, root string) (int64, error) {
		mu.Lock()
		walks[root]++
		mu.Unlock()
		// Any other request (scheduler, WebUI) needs the pool's only connection.
		qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var one int
		if err := a.DB.QueryRowContext(qctx, `SELECT 1`).Scan(&one); err != nil {
			return 0, fmt.Errorf("database held during disk walk: %w", err)
		}
		return orig(ctx, root)
	}
	view, err := a.capabilityStorageRequest(context.Background(), "storage", webui.CapabilityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	usage := view.(capabilityStorageView)
	if len(usage.Tools) != 3 {
		t.Fatalf("tools %+v", usage.Tools)
	}
	for _, tool := range usage.Tools {
		if tool.Directory != dir || tool.Bytes == 0 || tool.References != 4 {
			t.Fatalf("tool %+v", tool)
		}
	}
	if len(walks) != 1 || walks[dir] != 1 {
		t.Fatalf("walks %v, want one walk of %s", walks, dir)
	}
}

// Cleanup needs references, not sizes: it does not walk tool directories.
func TestCapabilityStorageGCSkipsToolSizes(t *testing.T) {
	a := newTestApp(t)
	storageRuntimeFixture(t, a, 10, 2)
	orig := capabilityStorageWalk
	t.Cleanup(func() { capabilityStorageWalk = orig })
	capabilityStorageWalk = func(context.Context, string) (int64, error) {
		t.Error("storage cleanup walked a tool directory for sizes")
		return 0, nil
	}
	if _, err := a.capabilityStorageRequest(context.Background(), "storage_gc", webui.CapabilityRequest{Reviewed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "runtimes", "node", "22.11.0")); err != nil {
		t.Fatal("referenced runtime removed:", err)
	}
}

// TIDEBOT_PERF=1: how long a concurrent DB query waits while the Extensions ›
// Tools page computes storage for a large managed runtime.
func TestCapabilityStorageViewPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	a := newTestApp(t)
	storageRuntimeFixture(t, a, 20000, 6)
	for round := 0; round < 3; round++ {
		start := time.Now()
		done := make(chan time.Duration, 1)
		go func() {
			time.Sleep(5 * time.Millisecond)
			q := time.Now()
			var one int
			_ = a.DB.QueryRow(`SELECT 1`).Scan(&one)
			done <- time.Since(q)
		}()
		if _, err := a.capabilityStorageRequest(context.Background(), "storage", webui.CapabilityRequest{}); err != nil {
			t.Fatal(err)
		}
		total := time.Since(start)
		t.Logf("round %d: storage view %v, concurrent SELECT 1 waited %v", round, total.Round(time.Millisecond), (<-done).Round(time.Microsecond))
	}
}
