package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// fakeUsage stands in for the meter: each bump is one more request.
type fakeUsage struct {
	mu    sync.Mutex
	reqs  int64
	saved pricing.Usage
	saves []int64
}

func (f *fakeUsage) bump() { f.mu.Lock(); f.reqs++; f.mu.Unlock() }
func (f *fakeUsage) snap() (pricing.Cost, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := pricing.Cost{Totals: pricing.Usage{Requests: f.reqs, Input: 100 * f.reqs}}
	if !usageChanged(c.Totals, f.saved) {
		return c, false
	}
	f.saved = c.Totals
	return c, true
}
func (f *fakeUsage) save(c pricing.Cost) { f.mu.Lock(); f.saves = append(f.saves, c.Totals.Requests); f.mu.Unlock() }
func (f *fakeUsage) got() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.saves...)
}

func waitSaves(t *testing.T, f *fakeUsage, n int) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for len(f.got()) < n {
		if time.Now().After(end) {
			t.Fatalf("saves %v, want %d", f.got(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// First change saved at once, a burst collapses into one trailing save with the
// latest totals, no change means no write, and stop is idempotent.
func TestUsageSaverThrottle(t *testing.T) {
	f := &fakeUsage{}
	s := startUsageSaver(150*time.Millisecond, f.snap, f.save)
	f.bump()
	s.changed()
	waitSaves(t, f, 1) // leading edge, well before the interval
	for i := 0; i < 20; i++ {
		f.bump()
		s.changed()
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(f.got()); n != 1 {
		t.Fatalf("burst saved early: %v", f.got())
	}
	waitSaves(t, f, 2) // trailing edge
	if g := f.got(); g[0] != 1 || g[1] != 21 {
		t.Fatalf("saves %v, want [1 21]", g)
	}
	s.changed() // nothing new
	time.Sleep(250 * time.Millisecond)
	if n := len(f.got()); n != 2 {
		t.Fatalf("unchanged usage was written: %v", f.got())
	}
	s.stop()
	s.stop()
	s.changed() // after stop: must not block or panic
}

// stop waits for a save that is in progress, so the caller's final write can't be
// overtaken by a late mid-run one.
func TestUsageSaverStopWaitsForSave(t *testing.T) {
	inSave, release := make(chan struct{}), make(chan struct{})
	var finished bool
	s := startUsageSaver(time.Hour, func() (pricing.Cost, bool) { return pricing.Cost{}, true }, func(pricing.Cost) {
		close(inSave)
		<-release
		finished = true
	})
	s.changed()
	<-inSave
	stopped := make(chan struct{})
	go func() { s.stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned during a save")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-stopped
	if !finished {
		t.Fatal("save did not finish")
	}
}

// A nil saver (pricing off) is a no-op.
func TestUsageSaverNil(t *testing.T) {
	var s *usageSaver
	s.changed()
	s.stop()
}

// While a run is still going, its tokens and priced cost are already in the
// database and OnUsage has seen them; the final write still wins.
func TestRunUsageSavedMidRun(t *testing.T) {
	skipOnWindows(t)
	r, sqlDB, dir := drainRunner(t, 1)
	r.UsageSaveEvery = 50 * time.Millisecond
	bin := slowClaude(t, dir, "30")
	opts := drainOpts(bin, dir)
	var mu sync.Mutex
	var seen []pricing.Cost
	opts.OnUsage = func(_ string, c pricing.Cost) { mu.Lock(); seen = append(seen, c); mu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	runID := make(chan string, 1)
	opts.OnStart = func(id string) { runID <- id }
	done := make(chan Result, 1)
	go func() { res, _ := r.RunOnce(ctx, opts); done <- res }()
	id := <-runID

	want := 2 * (1000*8.0 + 100*28.0) / 1e6
	end := time.Now().Add(5 * time.Second)
	for {
		var st string
		var amt float64
		var reqs, in int64
		if err := sqlDB.QueryRow(`SELECT status, COALESCE(cost_amount,0), COALESCE(requests,0), COALESCE(input_tokens,0) FROM runs WHERE id=?`, id).Scan(&st, &amt, &reqs, &in); err != nil {
			t.Fatal(err)
		}
		if reqs == 2 {
			if st != "running" || abs(amt-want) > 1e-9 || in != 2000 {
				t.Fatalf("mid-run row: status=%s cost=%v in=%d", st, amt, in)
			}
			break
		}
		if time.Now().After(end) {
			t.Fatalf("usage not saved mid-run (requests=%d)", reqs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	n := len(seen)
	last := pricing.Cost{}
	if n > 0 {
		last = seen[n-1]
	}
	mu.Unlock()
	if n == 0 || last.Totals.Requests != 2 || last.Currency != pricing.CNY {
		t.Fatalf("OnUsage saw %d saves, last %+v", n, last.Totals)
	}
	cancel()
	res := <-done
	if res.Status != "canceled" || res.Cost.Totals.Requests != 2 {
		t.Fatalf("final %s %+v", res.Status, res.Cost.Totals)
	}
	var st string
	if err := sqlDB.QueryRow(`SELECT status FROM runs WHERE id=?`, id).Scan(&st); err != nil || st != "canceled" {
		t.Fatalf("final row %s %v", st, err)
	}
}

// UsageSaveEvery < 0 turns mid-run saves off.
func TestRunUsageSaveOff(t *testing.T) {
	r := &Runner{UsageSaveEvery: -1}
	if r.usageEvery() > 0 {
		t.Fatal("negative interval should disable saving")
	}
	if (&Runner{}).usageEvery() != DefaultUsageSaveEvery {
		t.Fatal("zero should mean the default")
	}
}
