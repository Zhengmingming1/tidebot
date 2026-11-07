package managed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func quotaTreeFixture(t testing.TB, root string, files int) {
	t.Helper()
	dir := filepath.Join(root, "runtimes", "node", "22.11.0", "lib")
	for i := 0; i < files; i++ {
		sub := filepath.Join(dir, fmt.Sprintf("p%03d", i/100))
		if i%100 == 0 {
			if err := os.MkdirAll(sub, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%d.js", i)), []byte("module.exports=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := filepath.Join(root, "envs", "demo", "v1", "node_modules", "pkg")
	if err := os.MkdirAll(env, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env, "index.js"), []byte("exports=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "cache", "npm")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "entry.tgz"), []byte("cache-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func countQuotaWalks(t testing.TB) (*atomic.Int64, func()) {
	t.Helper()
	var walks atomic.Int64
	orig := measureTree
	measureTree = func(ctx context.Context, root string) (int64, error) {
		walks.Add(1)
		return orig(ctx, root)
	}
	return &walks, func() { measureTree = orig }
}

func TestCheckQuotaCachesUnchangedTree(t *testing.T) {
	root := t.TempDir()
	quotaTreeFixture(t, root, 200)
	walks, restore := countQuotaWalks(t)
	defer restore()

	i := &Installer{DataDir: root}
	first, err := i.CheckQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stamp1, _ := quotaDiskStamp(root)
	firstWalks := walks.Load()
	if firstWalks == 0 {
		t.Fatal("expected an initial size walk")
	}
	second, err := i.CheckQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if walks.Load() != firstWalks {
		stamp2, _ := quotaDiskStamp(root)
		t.Fatalf("cached CheckQuota walked again: %d -> %d\nstamp before:\n%s\nstamp after:\n%s", firstWalks, walks.Load(), stamp1, stamp2)
	}
	if first.ToolsAndRuntimes != second.ToolsAndRuntimes || first.Cache != second.Cache {
		t.Fatalf("cached usage mismatch: %+v vs %+v", first, second)
	}
	if first.Environments["demo/v1"] == 0 || first.Environments["demo/v1"] != second.Environments["demo/v1"] {
		t.Fatalf("env usage = %v", first.Environments)
	}
	// UI uses unlimited quotas; sizes must still come from the same cache.
	ui := &Installer{DataDir: root, Quotas: Quotas{ToolsAndRuntimes: 1 << 62, Environment: 1 << 62, Cache: 1 << 62}}
	third, err := ui.CheckQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if walks.Load() != firstWalks {
		t.Fatalf("UI CheckQuota walked again: %d", walks.Load())
	}
	if third.ToolsAndRuntimes != first.ToolsAndRuntimes {
		t.Fatalf("UI usage = %d, want %d", third.ToolsAndRuntimes, first.ToolsAndRuntimes)
	}
}

func TestCheckQuotaInvalidatesWhenVersionAppears(t *testing.T) {
	root := t.TempDir()
	quotaTreeFixture(t, root, 50)
	walks, restore := countQuotaWalks(t)
	defer restore()

	i := &Installer{DataDir: root}
	if _, err := i.CheckQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := walks.Load()
	stage := filepath.Join(root, "runtimes", "node", ".install-test")
	if err := os.MkdirAll(filepath.Join(stage, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "bin", "node"), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := i.CheckQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if walks.Load() <= base {
		t.Fatalf("expected re-walk after stage appeared; walks=%d base=%d", walks.Load(), base)
	}
	if after.ToolsAndRuntimes <= 0 {
		t.Fatalf("usage = %+v", after)
	}
}

func TestCheckQuotaReappliesLimitsFromCache(t *testing.T) {
	root := t.TempDir()
	quotaTreeFixture(t, root, 20)
	walks, restore := countQuotaWalks(t)
	defer restore()

	probe := &Installer{DataDir: root, Quotas: Quotas{ToolsAndRuntimes: 1 << 62, Environment: 1 << 62, Cache: 1 << 62}}
	usage, err := probe.CheckQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := walks.Load()
	tight := &Installer{DataDir: root, Quotas: Quotas{ToolsAndRuntimes: usage.ToolsAndRuntimes - 1, Environment: 1 << 62, Cache: 1 << 62}}
	_, err = tight.CheckQuota(context.Background())
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("err = %v", err)
	}
	if walks.Load() != base {
		t.Fatalf("limit check walked again: %d", walks.Load())
	}
}

func TestCheckQuotaPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	root := t.TempDir()
	quotaTreeFixture(t, root, 20000)
	i := &Installer{DataDir: root}
	var cold, hot []time.Duration
	for round := 0; round < 3; round++ {
		// Force a miss by touching the runtime version directory mtime.
		version := filepath.Join(root, "runtimes", "node", "22.11.0")
		now := time.Now().Add(time.Duration(round+1) * time.Second)
		if err := os.Chtimes(version, now, now); err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		if _, err := i.CheckQuota(context.Background()); err != nil {
			t.Fatal(err)
		}
		cold = append(cold, time.Since(t0))
		t0 = time.Now()
		if _, err := i.CheckQuota(context.Background()); err != nil {
			t.Fatal(err)
		}
		hot = append(hot, time.Since(t0))
	}
	for round := range cold {
		t.Logf("round %d: cold %v, hot (cached) %v", round, cold[round], hot[round])
	}
}
