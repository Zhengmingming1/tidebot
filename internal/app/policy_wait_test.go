package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

func shortPolicyPoll(t *testing.T) {
	t.Helper()
	old := dirBusyPoll
	dirBusyPoll = 20 * time.Millisecond
	t.Cleanup(func() { dirBusyPoll = old })
}

func runStatusCounts(a *App, taskID string) (ok, other int) {
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status='succeeded'`, taskID).Scan(&ok)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status<>'succeeded'`, taskID).Scan(&other)
	return ok, other
}

// A finished run's cost still waiting for its ledger write refuses new runs
// ("accounting unavailable") until it lands, seconds later. A scheduled run that
// meets it waits and goes then: no failed run, no breaker strike (three of
// those used to pause the task as "unknown" failures).
func TestScheduledTaskWaitsForPendingLedger(t *testing.T) {
	a := newTestApp(t)
	shortPolicyPoll(t)
	ctx := context.Background()
	tid := mkTask(t, a, "cron")
	task, _ := a.Store.GetTask(ctx, tid)
	msg := "cost ledger persistence failed for run_x: database is locked"
	a.accountingFailure.Store(&msg)
	done := make(chan struct{})
	go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
	time.Sleep(150 * time.Millisecond)
	if n := runCount(a, tid); n != 0 {
		t.Fatalf("%d run rows while accounting was pending", n)
	}
	a.accountingFailure.Store(nil)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("scheduled run never went after the ledger was written")
	}
	ok, other := runStatusCounts(a, tid)
	if fails := a.Policy.Get(ctx, tid).ConsecutiveFailures; ok != 1 || other != 0 || fails != 0 {
		t.Fatalf("succeeded=%d other=%d fails=%d", ok, other, fails)
	}
	if got, _ := a.Store.GetTask(ctx, tid); got.Status != "active" {
		t.Fatalf("task status %q", got.Status)
	}
}

// While one scheduled run of a "skip" task waits to start (its folder busy, a
// ledger write pending), later firings must not pile up behind it: the waiting
// run has no run row yet, so the scheduler's overlap check doesn't see it, and
// every firing used to wait too and then run one after another.
func TestWaitingScheduledRunAbsorbsLaterFirings(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	shortPolicyPoll(t)
	ctx := context.Background()

	fire := func(t *testing.T, tid string, n int) *sync.WaitGroup {
		task, _ := a.Store.GetTask(ctx, tid)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); a.runWithPolicy(ctx, *task, "cron", "production", 0) }()
			time.Sleep(40 * time.Millisecond) // the next minute's firing
		}
		return &wg
	}
	finish := func(t *testing.T, wg *sync.WaitGroup) {
		ch := make(chan struct{})
		go func() { wg.Wait(); close(ch) }()
		select {
		case <-ch:
		case <-time.After(30 * time.Second):
			t.Fatal("firings never ended")
		}
	}

	t.Run("busy folder", func(t *testing.T) {
		dir := t.TempDir()
		real := editLockKey(dir)
		tid := taskInFolder(t, a, "skip-dir", dir, "edit")
		if !lockEditDir(real) {
			t.Fatal("folder already held")
		}
		wg := fire(t, tid, 5)
		time.Sleep(100 * time.Millisecond)
		unlockEditDir(real)
		finish(t, wg)
		if ok, other := runStatusCounts(a, tid); ok != 1 || other != 0 {
			t.Fatalf("5 firings while the folder was busy: succeeded=%d other=%d, want 1/0", ok, other)
		}
	})
	t.Run("pending ledger", func(t *testing.T) {
		tid := mkTask(t, a, "skip-ledger")
		msg := "cost ledger persistence failed for run_y: database is locked"
		a.accountingFailure.Store(&msg)
		wg := fire(t, tid, 5)
		time.Sleep(100 * time.Millisecond)
		a.accountingFailure.Store(nil)
		finish(t, wg)
		ok, other := runStatusCounts(a, tid)
		if fails := a.Policy.Get(ctx, tid).ConsecutiveFailures; ok != 1 || other != 0 || fails != 0 {
			t.Fatalf("5 firings while accounting was pending: succeeded=%d other=%d fails=%d, want 1/0/0", ok, other, fails)
		}
	})
	t.Run("free again", func(t *testing.T) {
		// Nothing waits: firings behave as before (each one runs when its turn comes).
		tid := mkTask(t, a, "skip-free")
		finish(t, fire(t, tid, 2))
		if ok, _ := runStatusCounts(a, tid); ok != 2 {
			t.Fatalf("succeeded=%d, want 2", ok)
		}
	})
}

func waitPolicy(t *testing.T, a *App, taskID, want string) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool { return a.PolicyWait(taskID) == want })
}

// A scheduled run waiting for a busy folder or a pending ledger write is
// queryable (the task page shows why); the reason clears when the run starts,
// when the task is paused, or when tidebot starts closing. Parallel tasks
// have no overlap lock, but they are still visible. The idle path is not
// marked as waiting.
func TestPolicyWaitReasonExposedWhileScheduledRunWaits(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	shortPolicyPoll(t)
	ctx := context.Background()

	t.Run("busy folder", func(t *testing.T) {
		dir := t.TempDir()
		real := editLockKey(dir)
		tid := taskInFolder(t, a, "wait-dir", dir, "edit")
		task, _ := a.Store.GetTask(ctx, tid)
		if !lockEditDir(real) {
			t.Fatal("folder already held")
		}
		done := make(chan struct{})
		go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		waitPolicy(t, a, tid, PolicyWaitDirBusy)
		if n := runCount(a, tid); n != 0 {
			unlockEditDir(real)
			t.Fatalf("%d run rows while waiting", n)
		}
		unlockEditDir(real)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("run never went after the folder was freed")
		}
		if a.PolicyWait(tid) != "" {
			t.Fatal("wait left behind after the run started")
		}
		if ok, other := runStatusCounts(a, tid); ok != 1 || other != 0 {
			t.Fatalf("succeeded=%d other=%d", ok, other)
		}
	})

	t.Run("pending ledger", func(t *testing.T) {
		tid := mkTask(t, a, "wait-ledger")
		task, _ := a.Store.GetTask(ctx, tid)
		msg := "cost ledger persistence failed for run_z: database is locked"
		a.accountingFailure.Store(&msg)
		done := make(chan struct{})
		go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		waitPolicy(t, a, tid, PolicyWaitAccountingPending)
		if n := runCount(a, tid); n != 0 {
			a.accountingFailure.Store(nil)
			t.Fatalf("%d run rows while waiting", n)
		}
		a.accountingFailure.Store(nil)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("run never went after the ledger was written")
		}
		if a.PolicyWait(tid) != "" {
			t.Fatal("wait left behind after the ledger was written")
		}
	})

	t.Run("reason changes mid-wait", func(t *testing.T) {
		dir := t.TempDir()
		real := editLockKey(dir)
		tid := taskInFolder(t, a, "wait-switch", dir, "edit")
		task, _ := a.Store.GetTask(ctx, tid)
		msg := "cost ledger persistence failed for run_w: database is locked"
		a.accountingFailure.Store(&msg)
		done := make(chan struct{})
		go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		waitPolicy(t, a, tid, PolicyWaitAccountingPending)
		if !lockEditDir(real) {
			a.accountingFailure.Store(nil)
			t.Fatal("folder already held")
		}
		a.accountingFailure.Store(nil)
		waitPolicy(t, a, tid, PolicyWaitDirBusy)
		unlockEditDir(real)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("run never went after both waits cleared")
		}
		if a.PolicyWait(tid) != "" {
			t.Fatal("wait left behind after the reason switched")
		}
	})

	t.Run("paused while waiting", func(t *testing.T) {
		tid := mkTask(t, a, "wait-pause")
		task, _ := a.Store.GetTask(ctx, tid)
		msg := "cost ledger persistence failed for run_p: database is locked"
		a.accountingFailure.Store(&msg)
		defer a.accountingFailure.Store(nil)
		done := make(chan struct{})
		go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		waitPolicy(t, a, tid, PolicyWaitAccountingPending)
		if err := a.Tasks.SetStatus(ctx, tid, "paused", "user"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("paused task kept waiting")
		}
		if a.PolicyWait(tid) != "" {
			t.Fatal("wait left behind after the task was paused")
		}
		if n := runCount(a, tid); n != 0 {
			t.Fatalf("paused task ran: %d", n)
		}
	})

	t.Run("closing while waiting", func(t *testing.T) {
		b := newTestApp(t)
		shortPolicyPoll(t)
		tid := mkTask(t, b, "wait-close")
		task, _ := b.Store.GetTask(ctx, tid)
		msg := "cost ledger persistence failed for run_c: database is locked"
		b.accountingFailure.Store(&msg)
		done := make(chan struct{})
		go func() { b.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		waitPolicy(t, b, tid, PolicyWaitAccountingPending)
		b.stopPolicyRuns()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("closing app left the wait running")
		}
		if b.PolicyWait(tid) != "" {
			t.Fatal("wait left behind after closing")
		}
		if n := runCount(b, tid); n != 0 {
			t.Fatalf("closed app ran: %d", n)
		}
	})

	t.Run("parallel still visible", func(t *testing.T) {
		dir := t.TempDir()
		real := editLockKey(dir)
		tid := taskInFolder(t, a, "wait-par", dir, "edit")
		task, _ := a.Store.GetTask(ctx, tid)
		task.OverlapPolicy = "parallel"
		if !lockEditDir(real) {
			t.Fatal("folder already held")
		}
		done := make(chan struct{})
		go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		waitPolicy(t, a, tid, PolicyWaitDirBusy)
		unlockEditDir(real)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("parallel run never went after the folder was freed")
		}
		if a.PolicyWait(tid) != "" {
			t.Fatal("parallel wait left behind")
		}
	})

	t.Run("not waiting on the normal path", func(t *testing.T) {
		tid := mkTask(t, a, "wait-free")
		task, _ := a.Store.GetTask(ctx, tid)
		done := make(chan struct{})
		go func() { a.runWithPolicy(ctx, *task, "cron", "production", 0); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("free run never finished")
		}
		if a.PolicyWait(tid) != "" {
			t.Fatal("idle run was marked as waiting")
		}
		if ok, _ := runStatusCounts(a, tid); ok != 1 {
			t.Fatalf("succeeded=%d", ok)
		}
	})
}
