package runner

import (
	"bytes"
	"testing"
	"time"
)

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func TestLogBell(t *testing.T) {
	r := New(nil, nil, 1)
	var buf bytes.Buffer
	w := r.LogWriter("a", &buf)

	// No watcher: writing keeps no state around.
	w.Write([]byte("x\n"))
	if r.LogWatchers() != 0 {
		t.Fatal("bell kept for an unwatched run")
	}

	c1, done1 := r.LogWait("a")
	c2, done2 := r.LogWait("a")
	cb, doneB := r.LogWait("b")
	if closed(c1) || closed(c2) || r.LogWatchers() != 2 {
		t.Fatalf("waiters %d", r.LogWatchers())
	}
	w.Write([]byte("y\n"))
	if !closed(c1) || !closed(c2) {
		t.Fatal("write did not wake the waiters")
	}
	if closed(cb) {
		t.Fatal("other run woken")
	}
	if buf.String() != "x\ny\n" {
		t.Fatalf("bytes %q", buf.String())
	}
	done1()
	done2()
	done2() // idempotent

	// A fresh wait after the ring gets a new, open channel.
	c3, done3 := r.LogWait("a")
	if closed(c3) {
		t.Fatal("stale channel reused")
	}
	// One waiter leaving keeps the bell for the other.
	c4, done4 := r.LogWait("a")
	done3()
	if r.LogWatchers() != 2 {
		t.Fatalf("watchers after one left: %d", r.LogWatchers())
	}
	done4()
	doneB()
	if r.LogWatchers() != 0 {
		t.Fatalf("leaked bells: %d", r.LogWatchers())
	}
	if closed(c4) {
		t.Fatal("done must not close the channel")
	}

	// release (end of run) wakes anyone left.
	c5, done5 := r.LogWait("a")
	r.bells.release("a")
	select {
	case <-c5:
	case <-time.After(time.Second):
		t.Fatal("release did not wake")
	}
	done5()
	if r.LogWatchers() != 0 {
		t.Fatal("bell left after release")
	}
}
