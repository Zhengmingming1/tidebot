package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func waitPos(t *testing.T, s *slots, id string, want int) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for s.position(id) != want {
		if time.Now().After(end) {
			t.Fatalf("%s position=%d want %d", id, s.position(id), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Runs get slots first come, first served; one leaving the line moves the
// others up, and a freed slot goes straight to the head of the line.
func TestSlotsFIFOAndLeave(t *testing.T) {
	s := &slots{max: 1}
	quit := make(chan struct{})
	if err := s.acquire(context.Background(), quit, "a", nil); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 3)
	ahead := map[string]chan int{"b": make(chan int, 1), "c": make(chan int, 1), "d": make(chan int, 1)}
	cctx, stopC := context.WithCancelCause(context.Background())
	start := func(id string, ctx context.Context) {
		go func() {
			err := s.acquire(ctx, quit, id, func(n int) { ahead[id] <- n })
			if err != nil {
				got <- id + ":" + err.Error()
				return
			}
			got <- id
		}()
	}
	start("b", context.Background())
	waitPos(t, s, "b", 1)
	start("c", cctx)
	waitPos(t, s, "c", 2)
	start("d", context.Background())
	waitPos(t, s, "d", 3)
	if <-ahead["b"] != 0 || <-ahead["c"] != 1 || <-ahead["d"] != 2 {
		t.Fatal("ahead counts")
	}
	stopC(ErrStoppedByUser)
	if g := <-got; g != "c:"+ErrStoppedByUser.Error() {
		t.Fatalf("c: %s", g)
	}
	if s.position("d") != 2 || s.position("c") != 0 {
		t.Fatalf("after leave: d=%d c=%d", s.position("d"), s.position("c"))
	}
	s.release() // a done → b
	if g := <-got; g != "b" {
		t.Fatalf("next = %s, want b", g)
	}
	if s.position("d") != 1 {
		t.Fatalf("d=%d", s.position("d"))
	}
	s.release() // b done → d
	if g := <-got; g != "d" {
		t.Fatalf("next = %s, want d", g)
	}
	s.release()
	if s.busy != 0 || len(s.waiting) != 0 {
		t.Fatalf("busy=%d waiting=%d", s.busy, len(s.waiting))
	}
	// Free slot, nobody waiting: taken at once, no "queued" callback.
	if err := s.acquire(context.Background(), quit, "e", func(int) { t.Fatal("queued") }); err != nil {
		t.Fatal(err)
	}
	// Quitting releases the line.
	go func() { got <- errString(s.acquire(context.Background(), quit, "f", nil)) }()
	waitPos(t, s, "f", 1)
	close(quit)
	if g := <-got; g != errQuit.Error() {
		t.Fatalf("quit: %s", g)
	}
}

func recvID(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("run never got a row")
		return ""
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A slot handed over just as the waiter gives up is passed on, not lost.
func TestSlotsGrantedWhileLeaving(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := &slots{max: 1}
		quit := make(chan struct{})
		_ = s.acquire(context.Background(), quit, "a", nil)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.acquire(ctx, quit, "b", nil) }()
		waitPos(t, s, "b", 1)
		go cancel()
		s.release()
		if err := <-done; err == nil {
			s.release()
		}
		s.mu.Lock()
		busy, n := s.busy, len(s.waiting)
		s.mu.Unlock()
		if busy != 0 || n != 0 {
			t.Fatalf("iteration %d: busy=%d waiting=%d", i, busy, n)
		}
	}
}

// A run waiting for a slot has a row (queued, counts as active for its task),
// a place in line and a log, can be stopped before it starts, and its timeout
// only starts once it has a slot.
func TestQueuedRunVisibleStoppableAndTimedFromStart(t *testing.T) {
	skipOnWindows(t)
	r, sqlDB, dir := drainRunner(t, 1)
	r.LogDir = t.TempDir()
	slow := slowClaude(t, dir, "1.2")
	fastDir := t.TempDir()
	fast := slowClaude(t, fastDir, "0")

	first := make(chan Result, 1)
	go func() { res, _ := r.RunOnce(context.Background(), drainOpts(slow, dir)); first <- res }()
	waitActive(t, r, 1)

	type out struct {
		res Result
		err error
	}
	// Queued run that gets stopped.
	o := drainOpts(fast, fastDir)
	ids := make(chan string, 1)
	o.OnStart = func(id string) { ids <- id }
	stopped := make(chan out, 1)
	go func() { res, err := r.RunOnce(context.Background(), o); stopped <- out{res, err} }()
	qid := recvID(t, ids)
	waitPos(t, &r.slots, qid, 1)
	var st string
	_ = sqlDB.QueryRow(`SELECT status FROM runs WHERE id=?`, qid).Scan(&st)
	if st != "queued" {
		t.Fatalf("status=%s", st)
	}
	var active int // what HasActiveRun (overlap policy) and the tray count
	_ = sqlDB.QueryRow(`SELECT COUNT(1) FROM runs WHERE status IN ('queued','running')`).Scan(&active)
	if active != 2 {
		t.Fatalf("active rows=%d", active)
	}
	if r.QueuePos(qid) != 1 {
		t.Fatalf("QueuePos=%d", r.QueuePos(qid))
	}

	// A second queued run, with a timeout shorter than the wait.
	o2 := drainOpts(fast, fastDir)
	o2.Timeout = 700 * time.Millisecond
	ids2 := make(chan string, 1)
	o2.OnStart = func(id string) { ids2 <- id }
	later := make(chan out, 1)
	go func() { res, err := r.RunOnce(context.Background(), o2); later <- out{res, err} }()
	lid := recvID(t, ids2)
	waitPos(t, &r.slots, lid, 2)

	if !r.Cancel(qid) {
		t.Fatal("cancel queued")
	}
	q := <-stopped
	if !errors.Is(q.err, ErrStoppedByUser) || q.res.Status != "canceled" || q.res.RunID != qid {
		t.Fatalf("stopped queued: %+v %v", q.res, q.err)
	}
	var msg string
	var startedNull bool
	_ = sqlDB.QueryRow(`SELECT status, error_message, started_at IS NULL FROM runs WHERE id=?`, qid).Scan(&st, &msg, &startedNull)
	if st != "canceled" || msg != db.RunStoppedByUserMsg || !startedNull {
		t.Fatalf("row %s %q startedNull=%v", st, msg, startedNull)
	}
	b, _ := os.ReadFile(r.LogPath(qid))
	if !strings.Contains(string(b), "queued: waiting for a free slot (0 ahead)") || !strings.HasSuffix(string(b), "[[end]]\n") {
		t.Fatalf("log: %q", b)
	}
	if r.QueuePos(lid) != 1 {
		t.Fatalf("second moved up to %d", r.QueuePos(lid))
	}

	if res := <-first; res.Status != "succeeded" {
		t.Fatalf("first: %s %s", res.Status, res.ErrorMessage)
	}
	l := <-later // waited > its 700ms timeout, still ran to the end
	if l.err != nil || l.res.Status != "succeeded" {
		t.Fatalf("queued run timed by its wait: %+v %v", l.res, l.err)
	}
	if r.Cancel(lid) {
		t.Fatal("cancel of a finished run reported true")
	}
}
