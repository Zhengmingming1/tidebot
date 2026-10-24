package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A run that waited for a slot is checked again by BeforeStart when it gets one:
// a "no" ends it as skipped (never started, slot passed on), a "yes" may tighten
// its options. A run that got a slot at once is not checked twice.
func TestBeforeStartRecheckOnSlot(t *testing.T) {
	skipOnWindows(t)
	r, sqlDB, dir := drainRunner(t, 1)
	r.LogDir = t.TempDir()
	slow := slowClaude(t, dir, "0.8")

	var firstCalls atomic.Int32
	o1 := drainOpts(slow, dir)
	o1.BeforeStart = func(context.Context, *Options) error { firstCalls.Add(1); return nil }
	first := make(chan Result, 1)
	go func() { res, _ := r.RunOnce(context.Background(), o1); first <- res }()
	waitActive(t, r, 1)

	type out struct {
		res Result
		err error
	}
	// Queued, turned away at its slot.
	o2 := drainOpts(slow, dir)
	ids := make(chan string, 1)
	o2.OnStart = func(id string) { ids <- id }
	o2.BeforeStart = func(context.Context, *Options) error { return errors.New("budget blocked: global/ daily cap reached") }
	skipped := make(chan out, 1)
	go func() { res, err := r.RunOnce(context.Background(), o2); skipped <- out{res, err} }()
	sid := recvID(t, ids)
	waitPos(t, &r.slots, sid, 1)

	// Queued behind it, allowed but with a tighter timeout than its run needs.
	o3 := drainOpts(slow, dir)
	ids3 := make(chan string, 1)
	o3.OnStart = func(id string) { ids3 <- id }
	var sawTask atomic.Bool
	o3.TaskID = ""
	o3.BeforeStart = func(_ context.Context, o *Options) error {
		sawTask.Store(o.Profile.Name == "zhipu")
		o.Timeout = 150 * time.Millisecond
		return nil
	}
	tightened := make(chan out, 1)
	go func() { res, err := r.RunOnce(context.Background(), o3); tightened <- out{res, err} }()
	tid := recvID(t, ids3)
	waitPos(t, &r.slots, tid, 2)

	if res := <-first; res.Status != "succeeded" || firstCalls.Load() != 0 {
		t.Fatalf("first: %s, BeforeStart calls=%d", res.Status, firstCalls.Load())
	}
	s := <-skipped
	if !errors.Is(s.err, ErrSkipped) || s.res.Status != "skipped" || !strings.Contains(s.err.Error(), "budget blocked") {
		t.Fatalf("skipped: %+v %v", s.res, s.err)
	}
	var st, msg string
	var startedNull, finishedNull bool
	_ = sqlDB.QueryRow(`SELECT status, error_message, started_at IS NULL, finished_at IS NULL FROM runs WHERE id=?`, sid).Scan(&st, &msg, &startedNull, &finishedNull)
	if st != "skipped" || msg != "skipped: budget blocked: global/ daily cap reached" || !startedNull || finishedNull {
		t.Fatalf("row %s %q startedNull=%v finishedNull=%v", st, msg, startedNull, finishedNull)
	}
	b, _ := os.ReadFile(r.LogPath(sid))
	if !strings.Contains(string(b), "skipped before it started") || !strings.HasSuffix(string(b), "[[end]]\n") {
		t.Fatalf("log: %q", b)
	}

	l := <-tightened // got the slot the skipped run passed on; the new timeout applied
	if l.res.Status != "timeout" || !sawTask.Load() {
		t.Fatalf("tightened: %+v %v saw=%v", l.res, l.err, sawTask.Load())
	}
	if r.Active() != 0 {
		t.Fatalf("active=%d", r.Active())
	}
	r.slots.mu.Lock()
	busy := r.slots.busy
	r.slots.mu.Unlock()
	if busy != 0 {
		t.Fatalf("slot leaked: busy=%d", busy)
	}
}

// StartCheck runs for every run, queued or not, before BeforeStart: a "no" (e.g. the
// data disk is almost full) ends the run as skipped and frees the slot.
func TestStartCheckSkipsUnqueuedRun(t *testing.T) {
	skipOnWindows(t)
	r, sqlDB, dir := drainRunner(t, 1)
	r.LogDir = t.TempDir()
	o := drainOpts(slowClaude(t, dir, "0"), dir)
	ids := make(chan string, 1)
	o.OnStart = func(id string) { ids <- id }
	var before atomic.Int32
	o.BeforeStart = func(context.Context, *Options) error { before.Add(1); return nil }
	o.StartCheck = func(context.Context) error { return errors.New("disk full: 1/2") }
	res, err := r.RunOnce(context.Background(), o)
	id := recvID(t, ids)
	if !errors.Is(err, ErrSkipped) || res.Status != "skipped" || before.Load() != 0 {
		t.Fatalf("res %+v err %v before=%d", res, err, before.Load())
	}
	var msg string
	var startedNull bool
	_ = sqlDB.QueryRow(`SELECT error_message, started_at IS NULL FROM runs WHERE id=?`, id).Scan(&msg, &startedNull)
	if msg != "skipped: disk full: 1/2" || !startedNull {
		t.Fatalf("row %q startedNull=%v", msg, startedNull)
	}
	// Check passes: the next run starts and succeeds in the freed slot.
	o.StartCheck = func(context.Context) error { return nil }
	o.OnStart = nil
	if res, err := r.RunOnce(context.Background(), o); err != nil || res.Status != "succeeded" {
		t.Fatalf("second: %+v %v", res, err)
	}
}
