package capability

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// gcFixture installs n versions of one capability and pads every snapshot with
// extra files, as after several updates of a large package.
func gcFixture(t testing.TB, s *Store, n, files int) []Record {
	t.Helper()
	ctx := context.Background()
	var out []Record
	for i := 0; i < n; i++ {
		r, err := s.Install(ctx, testLock(), testFiles())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
		for f := 0; f < files; f++ {
			dir := filepath.Join(s.DataDir, "capabilities", r.ID, r.VersionID, "pad", fmt.Sprintf("d%03d", f/200))
			if f%200 == 0 {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", f)), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}

// Measuring a snapshot must not hold the pool's only SQLite connection, and
// pinned snapshots are skipped without walking them.
func TestGCWalkDoesNotHoldDB(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	versions := gcFixture(t, s, 4, 10)
	// versions[3] current, versions[2] kept for rollback, versions[0] pinned.
	if err := s.Bind(ctx, Binding{OwnerKind: "task", OwnerID: "task", CapabilityID: "example", VersionID: versions[0].VersionID}); err != nil {
		t.Fatal(err)
	}
	orig := gcSnapshotSize
	t.Cleanup(func() { gcSnapshotSize = orig })
	var walked []string
	gcSnapshotSize = func(ctx context.Context, target string) (int64, error) {
		walked = append(walked, filepath.Base(target))
		qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var one int
		if err := s.DB.QueryRowContext(qctx, `SELECT 1`).Scan(&one); err != nil {
			return 0, fmt.Errorf("database held during snapshot walk: %w", err)
		}
		return orig(ctx, target)
	}
	gc, err := s.GC(ctx, "example", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.RemovedVersions) != 1 || gc.RemovedVersions[0] != versions[1].VersionID || gc.BytesFreed <= 20 {
		t.Fatal(gc)
	}
	if len(walked) != 1 || walked[0] != versions[1].VersionID {
		t.Fatalf("walked %v", walked)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "capabilities", "example", versions[0].VersionID)); err != nil {
		t.Fatal("pinned snapshot removed", err)
	}
}

// A version that becomes referenced between the walk and the deletion
// transaction is kept.
func TestGCRechecksReferencesAfterWalk(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	versions := gcFixture(t, s, 3, 0)
	orig := gcSnapshotSize
	t.Cleanup(func() { gcSnapshotSize = orig })
	gcSnapshotSize = func(ctx context.Context, target string) (int64, error) {
		if err := s.Bind(ctx, Binding{OwnerKind: "task", OwnerID: "late", CapabilityID: "example", VersionID: filepath.Base(target)}); err != nil {
			return 0, err
		}
		return orig(ctx, target)
	}
	gc, err := s.GC(ctx, "example", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.RemovedVersions) != 0 {
		t.Fatal("removed a snapshot bound during the walk", gc)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "capabilities", "example", versions[0].VersionID)); err != nil {
		t.Fatal(err)
	}
}

// A snapshot replaced by a symlink is refused before anything is renamed.
func TestGCRefusesSymlinkSnapshot(t *testing.T) {
	s := testStore(t)
	versions := gcFixture(t, s, 3, 0)
	target := filepath.Join(s.DataDir, "capabilities", "example", versions[0].VersionID)
	outside := t.TempDir()
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := s.GC(context.Background(), "example", "ui"); err == nil {
		t.Fatal("GC followed a symlink snapshot")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Versions(context.Background(), "example")
	if len(list) != 3 {
		t.Fatalf("versions = %d", len(list))
	}
}

// TIDEBOT_PERF=1 go test ./internal/capability -run TestGCPerf -v
func TestGCPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	for round := 0; round < 3; round++ {
		s := testStore(t)
		gcFixture(t, s, 6, 20000)
		var mu sync.Mutex
		var maxWait time.Duration
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				t0 := time.Now()
				var one int
				_ = s.DB.QueryRow(`SELECT 1`).Scan(&one)
				w := time.Since(t0)
				mu.Lock()
				if w > maxWait {
					maxWait = w
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
			}
		}()
		t0 := time.Now()
		gc, err := s.GC(context.Background(), "example", "ui")
		total := time.Since(t0)
		close(stop)
		wg.Wait()
		if err != nil || len(gc.RemovedVersions) != 4 {
			t.Fatal(gc, err)
		}
		t.Logf("round %d: GC %v (4 snapshots × 20000 files), max concurrent SELECT 1 wait %v", round, total, maxWait)
	}
}
