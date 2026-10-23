package runner

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

// slowClaude streams two priced requests, then hangs (a long agent turn).
func slowClaude(t *testing.T, dir string, hang string) string {
	t.Helper()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		`echo '{"type":"assistant","message":{"id":"m1","model":"glm-5.3","usage":{"input_tokens":1000,"output_tokens":100}}}'` + "\n" +
		`echo '{"type":"assistant","message":{"id":"m2","model":"glm-5.3","usage":{"input_tokens":1000,"output_tokens":100}}}'` + "\n" +
		"sleep " + hang + "\n" +
		`echo '{"type":"result","subtype":"success","total_cost_usd":0.01,"usage":{"input_tokens":2000,"output_tokens":200}}'` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func drainRunner(t *testing.T, slots int) (*Runner, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return New(db.NewStore(sqlDB), nil, slots), sqlDB, dir
}

func drainOpts(bin, dir string) Options {
	prices := []pricing.ModelPrice{{Model: "glm-5.3", Currency: pricing.CNY, Input: pricing.F(8), Output: pricing.F(28)}}
	return Options{
		ClaudeBin: bin, Prompt: "x", WorkspaceDir: dir, Timeout: time.Minute,
		Profile: provider.Profile{Name: "zhipu", BaseURL: "http://127.0.0.1:9", Model: "glm-5.3"},
		Cost:    &CostControl{Calc: pricing.Calculator{Prices: prices}, FX: pricing.DefaultFX, Currency: pricing.CNY},
	}
}

func waitActive(t *testing.T, r *Runner, n int) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for r.Active() != n || (n > 0 && len(r.cancelIDs()) == 0) {
		if time.Now().After(end) {
			t.Fatalf("active=%d want %d", r.Active(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *Runner) cancelIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id := range r.cancels {
		if r.slots.position(id) == 0 { // running, not waiting for a slot
			ids = append(ids, id)
		}
	}
	return ids
}

// A run still going after the grace period is stopped, recorded as canceled
// with the quit message and the tokens it used so far, and quitting is bounded.
func TestDrainStopsLongRunAndKeepsUsage(t *testing.T) {
	skipOnWindows(t)
	r, sqlDB, dir := drainRunner(t, 2)
	bin := slowClaude(t, dir, "30")
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := r.RunOnce(context.Background(), drainOpts(bin, dir))
		done <- out{res, err}
	}()
	waitActive(t, r, 1)
	time.Sleep(300 * time.Millisecond) // let both usage lines be read

	start := time.Now()
	dr := r.Drain(200*time.Millisecond, 5*time.Second)
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("drain took %v", took)
	}
	if dr.Active != 1 || len(dr.Stopped) != 1 || dr.TimedOut {
		t.Fatalf("drain %+v", dr)
	}
	o := <-done
	if !errors.Is(o.err, ErrShuttingDown) || o.res.Status != "canceled" || o.res.ErrorMessage != db.RunStoppedOnQuitMsg {
		t.Fatalf("err=%v status=%s msg=%q", o.err, o.res.Status, o.res.ErrorMessage)
	}
	var st, msg string
	var amt float64
	if err := sqlDB.QueryRow(`SELECT status, error_message, cost_amount FROM runs WHERE id=?`, o.res.RunID).Scan(&st, &msg, &amt); err != nil {
		t.Fatal(err)
	}
	want := 2 * (1000*8.0 + 100*28.0) / 1e6
	if st != "canceled" || msg != db.RunStoppedOnQuitMsg || abs(amt-want) > 1e-9 {
		t.Fatalf("stored %s %q %v (want cost %v)", st, msg, amt, want)
	}
}

// A run that finishes inside the grace period is left alone.
func TestDrainWaitsForQuickRun(t *testing.T) {
	skipOnWindows(t)
	r, _, dir := drainRunner(t, 2)
	bin := slowClaude(t, dir, "0.4")
	done := make(chan Result, 1)
	go func() {
		res, _ := r.RunOnce(context.Background(), drainOpts(bin, dir))
		done <- res
	}()
	waitActive(t, r, 1)
	dr := r.Drain(10*time.Second, time.Second)
	if len(dr.Stopped) != 0 {
		t.Fatalf("stopped a run that was about to finish: %+v", dr)
	}
	if res := <-done; res.Status != "succeeded" {
		t.Fatalf("status=%s %s", res.Status, res.ErrorMessage)
	}
}

// After Drain starts, new runs are refused before any row is written and runs
// waiting for a concurrency slot give up instead of starting late (their row,
// written when they were queued, ends as canceled by the quit).
func TestDrainRefusesNewAndQueuedRuns(t *testing.T) {
	skipOnWindows(t)
	r, sqlDB, dir := drainRunner(t, 1)
	bin := slowClaude(t, dir, "30")
	first := make(chan error, 1)
	queued := make(chan error, 1)
	go func() { _, err := r.RunOnce(context.Background(), drainOpts(bin, dir)); first <- err }()
	waitActive(t, r, 1)
	go func() { _, err := r.RunOnce(context.Background(), drainOpts(bin, dir)); queued <- err }()
	end := time.Now().Add(5 * time.Second)
	for r.Active() != 2 {
		if time.Now().After(end) {
			t.Fatal("second run never queued")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Drain(0, 5*time.Second)
	if err := <-queued; !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("queued run: %v", err)
	}
	if err := <-first; !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("running run: %v", err)
	}
	if _, err := r.RunOnce(context.Background(), drainOpts(bin, dir)); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("new run after drain: %v", err)
	}
	var n, never int
	_ = sqlDB.QueryRow(`SELECT COUNT(1) FROM runs`).Scan(&n)
	_ = sqlDB.QueryRow(`SELECT COUNT(1) FROM runs WHERE status='canceled' AND started_at IS NULL AND error_message=?`, db.RunStoppedOnQuitMsg).Scan(&never)
	if n != 2 || never != 1 {
		t.Fatalf("runs=%d never-started=%d, want the running one plus the queued one closed by the quit", n, never)
	}
	if r.Active() != 0 {
		t.Fatalf("active=%d", r.Active())
	}
	// A second Drain (desktop calls Stop then Close) returns at once.
	if dr := r.Drain(time.Minute, time.Minute); dr.Active != 0 {
		t.Fatalf("second drain %+v", dr)
	}
}

// A user stop is its own cause, not a quit.
func TestUserCancelIsNotQuit(t *testing.T) {
	skipOnWindows(t)
	r, _, dir := drainRunner(t, 1)
	bin := slowClaude(t, dir, "30")
	done := make(chan Result, 1)
	var err error
	go func() {
		var res Result
		res, err = r.RunOnce(context.Background(), drainOpts(bin, dir))
		done <- res
	}()
	waitActive(t, r, 1)
	r.Cancel(r.cancelIDs()[0])
	res := <-done
	if !errors.Is(err, ErrStoppedByUser) || res.Status != "canceled" || res.ErrorMessage != db.RunStoppedByUserMsg {
		t.Fatalf("err=%v status=%s msg=%q", err, res.Status, res.ErrorMessage)
	}
}

// A stop or quit that lands just before the process starts (exec refuses a done
// context) keeps its cause instead of turning into a plain "failed" run.
func TestCancelBeforeStartKeepsCause(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := slowClaude(t, dir, "0")
	for _, c := range []struct {
		cause  error
		status string
		msg    string
	}{
		{ErrStoppedByUser, "canceled", db.RunStoppedByUserMsg},
		{ErrShuttingDown, "canceled", db.RunStoppedOnQuitMsg},
	} {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(c.cause)
		res, err := execute(ctx, drainOpts(bin, dir), io.Discard, 0, nil)
		if !errors.Is(err, c.cause) || res.Status != c.status || res.ErrorMessage != c.msg {
			t.Errorf("%v: err=%v status=%s msg=%q", c.cause, err, res.Status, res.ErrorMessage)
		}
	}
}
