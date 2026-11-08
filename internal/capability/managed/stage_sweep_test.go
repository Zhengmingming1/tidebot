package managed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// interruptedFixture is what a crash or force quit during installs leaves: a
// promoted version next to a partial .install-* stage and a .download-* file.
func interruptedFixture(t testing.TB, root string, stageBytes, downloadBytes int) {
	t.Helper()
	write := func(p string, n int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "runtimes", "node", "22.11.0", "bin", "node"), 1000)
	write(filepath.Join(root, "runtimes", "node", ".install-111", "lib", "a.js"), stageBytes/2)
	write(filepath.Join(root, "runtimes", "node", ".install-111", "lib", "b.js"), stageBytes-stageBytes/2)
	write(filepath.Join(root, "runtimes", "node", ".download-222"), downloadBytes)
	write(filepath.Join(root, "envs", "cap1", ".install-333", "node_modules", "x", "i.js"), 500)
	write(filepath.Join(root, "envs", "cap1", "v1", "node_modules", "x", "i.js"), 500)
}

func TestSweepStagesRemovesInterruptedInstalls(t *testing.T) {
	root := t.TempDir()
	interruptedFixture(t, root, 3000, 2000)
	// Not ours: other dot entries and a link named like a stage stay.
	other := filepath.Join(root, "runtimes", "node", ".keep")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := false
	if runtime.GOOS != "windows" { // symlinks need privileges there
		if err := os.MkdirAll(filepath.Join(root, "tools", "x"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "tools", "x", ".install-link")); err != nil {
			t.Fatal(err)
		}
		linked = true
	}

	r, err := SweepStages(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 3 || r.FreedBytes != 3000+2000+500 {
		t.Fatalf("report %+v", r)
	}
	for _, p := range []string{"runtimes/node/.install-111", "runtimes/node/.download-222", "envs/cap1/.install-333"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Fatalf("%s left", p)
		}
	}
	for _, p := range []string{"runtimes/node/22.11.0/bin/node", "envs/cap1/v1/node_modules/x/i.js", "runtimes/node/.keep"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Fatalf("%s removed: %v", p, err)
		}
	}
	if linked {
		if _, err := os.Lstat(filepath.Join(root, "tools", "x", ".install-link")); err != nil {
			t.Fatal("linked stage should be skipped, not removed")
		}
		if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
			t.Fatal("link target touched")
		}
		if len(r.Skipped) != 1 {
			t.Fatalf("skipped %v", r.Skipped)
		}
	}
	// Nothing left: a second sweep is a no-op.
	if r, err = SweepStages(context.Background(), root); err != nil || len(r.Removed) != 0 {
		t.Fatalf("second sweep %+v %v", r, err)
	}
	// A fresh data directory without managed folders is fine.
	if _, err = SweepStages(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err = SweepStages(context.Background(), "relative"); err == nil {
		t.Fatal("relative data directory accepted")
	}
}

// Leftovers used to count against the quota forever (GC skips dot entries), so
// installs failed with ErrQuotaExceeded and storage cleanup could not help.
func TestLeftoverStagesNoLongerExhaustQuota(t *testing.T) {
	root := t.TempDir()
	interruptedFixture(t, root, 6000, 3000)
	i := &Installer{DataDir: root, Quotas: Quotas{ToolsAndRuntimes: 5000}}
	if _, err := i.CheckQuota(context.Background()); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("fixture should exceed the quota, got %v", err)
	}
	dry, err := i.GC(context.Background(), GCOptions{DryRun: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Stages) != 3 || dry.FreedBytes < 6000+3000+500 {
		t.Fatalf("dry run %+v", dry)
	}
	if _, err := os.Stat(filepath.Join(root, "runtimes", "node", ".install-111")); err != nil {
		t.Fatal("dry run removed a stage")
	}
	rep, err := i.GC(context.Background(), GCOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Stages) != 3 {
		t.Fatalf("gc %+v", rep)
	}
	u, err := i.CheckQuota(context.Background())
	if err != nil {
		t.Fatalf("quota after cleanup: %v", err)
	}
	if u.ToolsAndRuntimes != 1000 {
		t.Fatalf("usage %+v", u)
	}
	if _, ok := u.Environments["cap1/.install-333"]; ok || len(u.Environments) != 1 {
		t.Fatalf("environments %+v", u.Environments)
	}
}

// TIDEBOT_PERF=1: a force-quit Node bundle install (20 000 files) plus its
// download; quota usage before and after the startup sweep, and its time.
func TestSweepStagesPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	root := t.TempDir()
	quotaTreeFixture(t, root, 200)
	stage := filepath.Join(root, "runtimes", "node", ".install-999", "lib")
	for n := 0; n < 20000; n++ {
		sub := filepath.Join(stage, "p"+strconv.Itoa(n/500))
		if n%500 == 0 {
			if err := os.MkdirAll(sub, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(sub, "f"+strconv.Itoa(n)+".js"), make([]byte, 2048), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "runtimes", "node", ".download-999"), make([]byte, 30<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	i := &Installer{DataDir: root}
	before, _ := i.CheckQuota(context.Background())
	start := time.Now()
	r, err := SweepStages(context.Background(), root)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := i.CheckQuota(context.Background())
	t.Logf("tools+runtimes %d KB -> %d KB; removed %d, freed %d KB in %v", before.ToolsAndRuntimes>>10, after.ToolsAndRuntimes>>10, len(r.Removed), r.FreedBytes>>10, took)
}
