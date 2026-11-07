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

// snapshotTreeFixture fills capabilities/ with another capability's snapshot
// files, as after several installs and updates.
func snapshotTreeFixture(t testing.TB, s *Store, files int) {
	t.Helper()
	for i := 0; i < files; i++ {
		dir := filepath.Join(s.DataDir, "capabilities", "other", fmt.Sprintf("capver_%03d", i/200), "src")
		if i%200 == 0 {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.md", i)), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The quota walk must not hold the pool's only SQLite connection.
func TestInstallQuotaWalkDoesNotHoldDB(t *testing.T) {
	s := testStore(t)
	snapshotTreeFixture(t, s, 50)
	orig := snapshotUsage
	t.Cleanup(func() { snapshotUsage = orig })
	walks := 0
	snapshotUsage = func(st *Store, ctx context.Context) (int64, error) {
		walks++
		qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var one int
		if err := st.DB.QueryRowContext(qctx, `SELECT 1`).Scan(&one); err != nil {
			return 0, fmt.Errorf("database held during quota walk: %w", err)
		}
		return orig(st, ctx)
	}
	if _, err := s.Install(context.Background(), testLock(), testFiles()); err != nil {
		t.Fatal(err)
	}
	if walks != 1 {
		t.Fatalf("quota walks = %d", walks)
	}
}

// Concurrent installs stay serialized across the quota check, so two installs
// cannot both pass a quota that only one of them fits.
func TestInstallQuotaSerializesConcurrentInstalls(t *testing.T) {
	s := testStore(t)
	probe := testStore(t)
	if _, err := probe.Install(context.Background(), testLock(), testFiles()); err != nil {
		t.Fatal(err)
	}
	one, err := probe.SnapshotUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Room for one snapshot but not two.
	s.MaxSnapshotDiskBytes = one + one/2
	orig := snapshotUsage
	t.Cleanup(func() { snapshotUsage = orig })
	var mu sync.Mutex
	active, maxActive := 0, 0
	snapshotUsage = func(st *Store, ctx context.Context) (int64, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		return orig(st, ctx)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lock := testLock()
			lock.ID = fmt.Sprintf("example%d", i)
			_, errs[i] = s.Install(context.Background(), lock, testFiles())
		}(i)
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("concurrent quota walks = %d", maxActive)
	}
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("installs within quota = %d (%v)", ok, errs)
	}
	list, _ := s.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("installed = %d", len(list))
	}
}

// TIDEBOT_PERF=1 go test ./internal/capability -run TestInstallQuotaPerf -v
func TestInstallQuotaPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	s := testStore(t)
	snapshotTreeFixture(t, s, 20000)
	for round := 0; round < 3; round++ {
		started := make(chan struct{})
		var wait time.Duration
		done := make(chan struct{})
		orig := snapshotUsage
		snapshotUsage = func(st *Store, ctx context.Context) (int64, error) {
			close(started)
			return orig(st, ctx)
		}
		go func() {
			defer close(done)
			<-started
			time.Sleep(time.Millisecond)
			t0 := time.Now()
			var one int
			_ = s.DB.QueryRow(`SELECT 1`).Scan(&one)
			wait = time.Since(t0)
		}()
		t0 := time.Now()
		lock := testLock()
		lock.ID = fmt.Sprintf("perf%d", round)
		if _, err := s.Install(context.Background(), lock, testFiles()); err != nil {
			t.Fatal(err)
		}
		total := time.Since(t0)
		<-done
		snapshotUsage = orig
		t.Logf("round %d: install %v, concurrent SELECT 1 waited %v", round, total, wait)
	}
}
