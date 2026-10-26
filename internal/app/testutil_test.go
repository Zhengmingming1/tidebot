package app

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/power"
)

func mockClaude() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", "mock-claude", "claude")
}

func newTestApp(t testing.TB) *App {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock claude is a shell script; app tests run on unix")
	}
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
	cfg.LogFormat = "text"
	a, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	// Tests don't depend on the CI machine's battery (power saving tests set their own).
	a.power.read = func() power.State { return power.Unknown }
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
