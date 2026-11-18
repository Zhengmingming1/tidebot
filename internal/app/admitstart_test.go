package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func testRetryApp() *App {
	return &App{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func withDayBudget(t *testing.T, a *App, usd float64) {
	t.Helper()
	if err := a.Cost.UpsertBudget(context.Background(), cost.Budget{
		Scope: "global", Period: "day", Action: "skip", Currency: pricing.USD,
		MaxUSD: sql.NullFloat64{Float64: usd, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
}

func heldCount(a *App) int {
	_, n := a.Cost.Held("global", "", "", pricing.USD)
	return n
}

func admitStartOpts(id string) *runner.Options {
	return &runner.Options{RunID: id}
}

func TestRetryStartOpUsesIndependentContext(t *testing.T) {
	a := testRetryApp()
	runCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var usedRun bool
	err := a.retryStartOp(runCtx, "test", "id", func(wctx context.Context) error {
		if wctx == runCtx {
			usedRun = true
		}
		if _, ok := wctx.Deadline(); !ok {
			t.Fatal("write context has no deadline")
		}
		if wctx.Err() != nil {
			t.Fatalf("write context already done: %v", wctx.Err())
		}
		cancel(runner.ErrStoppedByUser)
		if wctx.Err() != nil {
			t.Fatal("write context canceled with the run context")
		}
		return nil
	})
	if usedRun {
		t.Fatal("used runCtx for the database call")
	}
	if !errors.Is(err, runner.ErrStoppedByUser) {
		t.Fatalf("got %v", err)
	}
}

func TestRetryStartOpRetriesBusyThenSucceeds(t *testing.T) {
	shortWriteRetries(t)
	a := testRetryApp()
	var n atomic.Int32
	start := time.Now()
	err := a.retryStartOp(context.Background(), "test", "id", func(context.Context) error {
		if n.Add(1) == 1 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("attempts %d", n.Load())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("retry took too long: %s", time.Since(start))
	}
}

func TestRetryStartOpGivesUpWhenStillBusy(t *testing.T) {
	wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
	t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
	writeRetryWait, writeRetryPause, writeRetryFor = 20*time.Millisecond, time.Millisecond, 40*time.Millisecond

	a := testRetryApp()
	err := a.retryStartOp(context.Background(), "test", "id", func(context.Context) error {
		return errors.New("database is locked")
	})
	if err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("got %v", err)
	}
}

func TestRetryStartOpDoesNotRetryPermanentError(t *testing.T) {
	a := testRetryApp()
	var n atomic.Int32
	start := time.Now()
	err := a.retryStartOp(context.Background(), "test", "id", func(context.Context) error {
		n.Add(1)
		return errors.New("budget blocked: global daily cap reached")
	})
	if err == nil || !strings.Contains(err.Error(), "budget blocked") {
		t.Fatalf("got %v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("retried a permanent refusal: %d", n.Load())
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("permanent refusal retried: %s", time.Since(start))
	}
}

func TestRetryStartOpHonorsUserStopWhileWaiting(t *testing.T) {
	wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
	t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
	writeRetryWait, writeRetryPause, writeRetryFor = 20*time.Millisecond, 500*time.Millisecond, time.Minute

	a := testRetryApp()
	runCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var n atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- a.retryStartOp(runCtx, "test", "id", func(context.Context) error {
			n.Add(1)
			return errors.New("database is locked")
		})
	}()
	waitFor(t, 2*time.Second, func() bool { return n.Load() >= 1 })
	cancel(runner.ErrStoppedByUser)
	select {
	case err := <-done:
		if !errors.Is(err, runner.ErrStoppedByUser) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not honor user stop")
	}
}

func TestAdmitAtStartHoldsAndWritesSnapshot(t *testing.T) {
	a := newTestApp(t)
	insertTestRun(t, a, "as1")
	withDayBudget(t, a, 10)
	o := admitStartOpts("as1")
	start := time.Now()
	res, err := a.admitAtStart(context.Background(), o, cost.BucketProduction, modeMeteredProfile(), 0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if res.hold == nil || res.launchedID != "as1" || res.accountingDay == "" || o.Cost == nil {
		t.Fatalf("%+v cost=%v", res, o.Cost)
	}
	if heldCount(a) != 1 {
		t.Fatalf("holds %d", heldCount(a))
	}
	if err := a.saveCostSnapshot(o, cost.BucketProduction, res.accountingAt); err != nil {
		t.Fatal(err)
	}
	raw := readCostSnapshot(t, a, "as1")
	if !strings.Contains(raw, `"mode":"task"`) || !strings.Contains(raw, `"bucket":"production"`) {
		t.Fatalf("snapshot %s", raw)
	}
	t.Logf("admitAtStart happy path %s", elapsed)
	res.hold.Release()
}

func TestAdmitAtStartHonorsUserStop(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(runner.ErrStoppedByUser)
	if _, err := a.admitAtStart(ctx, admitStartOpts("as2"), cost.BucketProduction, modeMeteredProfile(), 0); !errors.Is(err, runner.ErrStoppedByUser) {
		t.Fatalf("got %v", err)
	}
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
}

func TestAdmitAtStartHonorsShutdown(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(runner.ErrShuttingDown)
	if _, err := a.admitAtStart(ctx, admitStartOpts("as3"), cost.BucketProduction, modeMeteredProfile(), 0); !errors.Is(err, runner.ErrShuttingDown) {
		t.Fatalf("got %v", err)
	}
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
}

func TestAdmitAtStartReleasesHoldIfStopAfterAdmit(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var res startReservation
	err := a.retryStartOp(ctx, "admit at start", "as4", func(wctx context.Context) error {
		if err := a.admitAtStartOnce(wctx, admitStartOpts("as4"), cost.BucketProduction, modeMeteredProfile(), 0, &res); err != nil {
			return err
		}
		cancel(runner.ErrStoppedByUser)
		return nil
	})
	if !errors.Is(err, runner.ErrStoppedByUser) {
		t.Fatalf("got %v", err)
	}
	// admitAtStart releases the reservation when retryStartOp returns a stop.
	a.releaseStartReservation(&res)
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
}

func TestReleaseStartReservationClearsHold(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	res, err := a.admitAtStart(context.Background(), admitStartOpts("as5"), cost.BucketProduction, modeMeteredProfile(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if heldCount(a) != 1 {
		t.Fatalf("holds %d", heldCount(a))
	}
	a.releaseStartReservation(&res)
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
	if res.hold != nil || res.launchedID != "" {
		t.Fatalf("not cleared: %+v", res)
	}
}

func TestAdmitAtStartBudgetBlockedIsNotRetried(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 1)
	if err := a.Cost.RecordEntry(context.Background(), cost.Entry{RunID: "spent-admit", Amount: 2, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := a.admitAtStart(context.Background(), admitStartOpts("as6"), cost.BucketProduction, modeMeteredProfile(), 0)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "budget blocked") {
		t.Fatalf("got %v", err)
	}
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("permanent refusal retried: %s", elapsed)
	}
	t.Logf("admitAtStart budget blocked %s", elapsed)
}

func TestAdmitAtStartOnceRetryDoesNotDoubleHold(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	var res startReservation
	o := admitStartOpts("as7")
	if err := a.admitAtStartOnce(context.Background(), o, cost.BucketProduction, modeMeteredProfile(), 0, &res); err != nil {
		t.Fatal(err)
	}
	if heldCount(a) != 1 {
		t.Fatalf("holds %d", heldCount(a))
	}
	first := res.hold
	if err := a.admitAtStartOnce(context.Background(), o, cost.BucketProduction, modeMeteredProfile(), 0, &res); err != nil {
		t.Fatal(err)
	}
	if heldCount(a) != 1 {
		t.Fatalf("double hold: %d", heldCount(a))
	}
	if res.hold == first {
		t.Fatal("did not replace hold")
	}
	a.releaseStartReservation(&res)
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
}

func TestRetryableStartErr(t *testing.T) {
	if retryableStartErr(nil) || retryableStartErr(errors.New("budget blocked: cap")) || retryableStartErr(errBudgetHeld) || retryableStartErr(errAccountingPending) || retryableStartErr(errTaskInactive) {
		t.Fatal("permanent errors must not retry")
	}
	pendingBusy := fmt.Errorf("%w: cost ledger persistence failed for run_y: database is locked", errAccountingPending)
	if retryableStartErr(pendingBusy) {
		t.Fatal("pending ledger must not be retried as a busy database")
	}
	if retryableStartErr(fmt.Errorf("%w: %w", errBudgetHeld, errors.New("database is locked"))) {
		t.Fatal("held budget must not be retried as a busy database")
	}
	if !retryableStartErr(context.DeadlineExceeded) || !retryableStartErr(context.Canceled) || !retryableStartErr(errors.New("database is locked")) {
		t.Fatal("busy/timeout must retry")
	}
}

func countAllRuns(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdmitRunRetrySucceedsAndDoesNotHold(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	start := time.Now()
	runMax, cc, err := a.admitRunRetry(context.Background(), "admit before queue", "", "", cost.BucketProduction, modeMeteredProfile(), 0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if cc == nil || runMax <= 0 {
		t.Fatalf("cap %v cost=%v", runMax, cc)
	}
	if heldCount(a) != 0 {
		t.Fatalf("pre-queue admit must not hold budget: %d", heldCount(a))
	}
	t.Logf("admitRunRetry happy path %s", elapsed)
}

func TestAdmitRunRetryBudgetBlockedIsNotRetried(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 1)
	if err := a.Cost.RecordEntry(context.Background(), cost.Entry{RunID: "spent-prequeue", Amount: 2, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err := a.admitRunRetry(context.Background(), "admit before queue", "", "", cost.BucketProduction, modeMeteredProfile(), 0)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "budget blocked") {
		t.Fatalf("got %v", err)
	}
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("permanent refusal retried: %s", elapsed)
	}
	t.Logf("admitRunRetry budget blocked %s", elapsed)
}

func TestAdmitRunRetryHonorsUserStop(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(runner.ErrStoppedByUser)
	if _, _, err := a.admitRunRetry(ctx, "admit before queue", "", "", cost.BucketProduction, modeMeteredProfile(), 0); !errors.Is(err, runner.ErrStoppedByUser) {
		t.Fatalf("got %v", err)
	}
	if heldCount(a) != 0 {
		t.Fatalf("hold leaked: %d", heldCount(a))
	}
}

func TestRunPromptExBudgetBlockedDoesNotQueue(t *testing.T) {
	a := newTestApp(t)
	withDayBudget(t, a, 1)
	if err := a.Cost.RecordEntry(context.Background(), cost.Entry{RunID: "spent-runprompt", Amount: 2, Currency: pricing.USD, Source: pricing.SourceClaude}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "must not start", TriggerKind: "manual"}, "anthropic", 0)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "budget blocked") {
		t.Fatalf("got %+v %v", res, err)
	}
	if res.RunID != "" || countAllRuns(t, a) != 0 {
		t.Fatalf("queued a run before admission: %+v count=%d", res, countAllRuns(t, a))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("permanent refusal retried: %s", elapsed)
	}
}

func TestRunPromptExHonorsCanceledContextBeforeQueue(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(runner.ErrStoppedByUser)
	res, err := a.RunPromptEx(ctx, runner.Options{Prompt: "must not start", TriggerKind: "manual"}, "anthropic", 0)
	if !errors.Is(err, runner.ErrStoppedByUser) {
		t.Fatalf("got %+v %v", res, err)
	}
	if res.RunID != "" || countAllRuns(t, a) != 0 {
		t.Fatalf("queued a run after stop: %+v count=%d", res, countAllRuns(t, a))
	}
}

func TestAdmitRunRetryRetriesBusyThenSucceeds(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	var n atomic.Int32
	admitRunHook = func() error {
		if n.Add(1) == 1 {
			return errors.New("database is locked")
		}
		return nil
	}
	t.Cleanup(func() { admitRunHook = nil })
	start := time.Now()
	runMax, cc, err := a.admitRunRetry(context.Background(), "admit before queue", "", "", cost.BucketProduction, modeMeteredProfile(), 0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 || cc == nil || runMax <= 0 {
		t.Fatalf("attempts %d cap %v cost=%v", n.Load(), runMax, cc)
	}
	if heldCount(a) != 0 {
		t.Fatalf("pre-queue admit must not hold budget: %d", heldCount(a))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("retry took too long: %s", elapsed)
	}
}

func TestRunPromptExFirstAdmitRetriesWhenDatabaseBusy(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	withDayBudget(t, a, 10)
	var n atomic.Int32
	admitRunHook = func() error {
		if n.Add(1) == 1 {
			return errors.New("database is locked")
		}
		return nil
	}
	t.Cleanup(func() { admitRunHook = nil })
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "hello", TriggerKind: "manual"}, "anthropic", 0)
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("%+v %v", res, err)
	}
	if n.Load() < 2 {
		t.Fatalf("first admit was not retried: %d", n.Load())
	}
	if res.RunID == "" || countAllRuns(t, a) == 0 {
		t.Fatalf("run not queued after retry: %+v count=%d", res, countAllRuns(t, a))
	}
}

func TestRunPromptExFirstAdmitDoesNotRetryPermanentError(t *testing.T) {
	a := newTestApp(t)
	var n atomic.Int32
	admitRunHook = func() error {
		n.Add(1)
		return errors.New("budget blocked: global daily cap reached")
	}
	t.Cleanup(func() { admitRunHook = nil })
	start := time.Now()
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "must not start", TriggerKind: "manual"}, "anthropic", 0)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "budget blocked") {
		t.Fatalf("got %+v %v", res, err)
	}
	if n.Load() != 1 {
		t.Fatalf("retried a permanent refusal: %d", n.Load())
	}
	if res.RunID != "" || countAllRuns(t, a) != 0 {
		t.Fatalf("queued a run after a permanent refusal: %+v count=%d", res, countAllRuns(t, a))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("permanent refusal retried: %s", elapsed)
	}
}

func TestAdmitRunRetryAccountingPendingIsNotRetried(t *testing.T) {
	a := newTestApp(t)
	msg := "cost ledger persistence failed for run_y: database is locked"
	a.accountingFailure.Store(&msg)
	t.Cleanup(func() { a.accountingFailure.Store(nil) })
	start := time.Now()
	_, _, err := a.admitRunRetry(context.Background(), "admit before queue", "", "", cost.BucketProduction, modeMeteredProfile(), 0)
	elapsed := time.Since(start)
	if !errors.Is(err, errAccountingPending) {
		t.Fatalf("got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("pending ledger retried: %s", elapsed)
	}
}

func TestRunPromptExAccountingPendingDoesNotQueue(t *testing.T) {
	a := newTestApp(t)
	msg := "cost ledger persistence failed for run_y: database is locked"
	a.accountingFailure.Store(&msg)
	t.Cleanup(func() { a.accountingFailure.Store(nil) })
	start := time.Now()
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "must not start", TriggerKind: "manual"}, "anthropic", 0)
	elapsed := time.Since(start)
	if !errors.Is(err, errAccountingPending) {
		t.Fatalf("got %+v %v", res, err)
	}
	if res.RunID != "" || countAllRuns(t, a) != 0 {
		t.Fatalf("queued a run while accounting was pending: %+v count=%d", res, countAllRuns(t, a))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("pending ledger retried: %s", elapsed)
	}
}
