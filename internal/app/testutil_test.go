package app

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/config"
)

func mockClaude() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", "mock-claude", "claude")
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.HomeDir = dir
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.DBPath = filepath.Join(dir, "data", "t.db")
	cfg.IPCPath = filepath.Join(dir, "a.sock")
	cfg.ClaudeBin = mockClaude()
	cfg.DefaultProvider = "kimi-coding"
	cfg.DefaultTimeout = time.Minute
	cfg.LogLevel = "error"
	a, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.SeedBuiltinProviders(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = a.Cost.EnsureDefaults(context.Background())
	return a
}

func waitFor(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout")
}
