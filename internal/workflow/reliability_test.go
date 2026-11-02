package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeRetriesOnlyTransientFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		max    int
		fail   error
		calls  int
		status string
	}{
		{"default", 0, errors.New("503 server_error"), 1, "failed"},
		{"transient", 2, errors.New("503 server_error"), 3, "succeeded"},
		{"permanent", 2, errors.New("invalid request"), 1, "failed"},
		{"exhausted", 1, errors.New("503 server_error"), 2, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, downstream atomic.Int32
			s := service(t, func(ctx context.Context, n Node, _, _ string, _ func(string)) (string, error) {
				if n.ID == "next" {
					downstream.Add(1)
					return "done", nil
				}
				if calls.Add(1) < 3 {
					return "", tc.fail
				}
				return "ok", nil
			})
			n := skill("first")
			n.RetryMax = tc.max
			n.RetryBackoffMS = 1
			r := finished(t, s, start(t, s, save(t, s, n, skill("next", Dependency{NodeID: "first"}))))
			if r.Status != tc.status || calls.Load() != int32(tc.calls) {
				t.Fatalf("%+v calls=%d", r, calls.Load())
			}
			if (downstream.Load() == 1) != (tc.status == "succeeded") {
				t.Fatal("downstream executed before successful retry")
			}
		})
	}
}

func TestRetryCancelAndPanicAreTerminal(t *testing.T) {
	t.Run("cancel backoff", func(t *testing.T) {
		entered := make(chan struct{})
		var calls atomic.Int32
		s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) {
			calls.Add(1)
			close(entered)
			return "", errors.New("503")
		})
		n := skill("a")
		n.RetryMax = 5
		n.RetryBackoffMS = 60000
		rid := start(t, s, save(t, s, n))
		<-entered
		if err := s.Stop(rid); err != nil {
			t.Fatal(err)
		}
		if r := finished(t, s, rid); r.Status != "canceled" || calls.Load() != 1 {
			t.Fatalf("%+v calls=%d", r, calls.Load())
		}
	})
	t.Run("panic", func(t *testing.T) {
		s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) { panic("fixture-secret") })
		r := finished(t, s, start(t, s, save(t, s, skill("a"))))
		if r.Status != "failed" || !strings.Contains(r.Error, "panicked") || strings.Contains(r.Error, "fixture-secret") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestLateChildCallbackCannotOverwriteTerminalNode(t *testing.T) {
	callbacks := make(chan func(string), 1)
	s := service(t, func(_ context.Context, _ Node, _, _ string, cb func(string)) (string, error) {
		callbacks <- cb
		return "ok", nil
	})
	rid := start(t, s, save(t, s, skill("a")))
	r := finished(t, s, rid)
	cb := <-callbacks
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); cb("late") }()
	}
	wg.Wait()
	again, err := s.GetRun(context.Background(), rid)
	if err != nil || state(again, "a") != state(r, "a") {
		t.Fatalf("late callback changed result: %+v %v", again, err)
	}
}

func TestRecoveryRefusesLiveWorkflowAndIsIdempotent(t *testing.T) {
	entered := make(chan struct{})
	s := service(t, func(ctx context.Context, _ Node, _, _ string, _ func(string)) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	})
	rid := start(t, s, save(t, s, skill("a")))
	<-entered
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("recovered live work")
	}
	s.BeginShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	before := finished(t, s, rid)
	for i := 0; i < 2; i++ {
		if err := s.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.GetRun(ctx, rid)
	if err != nil || before.FinishedAt != after.FinishedAt || state(before, "a") != state(after, "a") {
		t.Fatal("recovery changed terminal work", err)
	}
}

func TestRetryBoundsAndSnapshot(t *testing.T) {
	for _, n := range []Node{{RetryMax: -1}, {RetryMax: 6}, {RetryBackoffMS: -1}, {RetryBackoffMS: 60001}} {
		node := skill("a")
		node.RetryMax = n.RetryMax
		node.RetryBackoffMS = n.RetryBackoffMS
		d := Definition{Name: "invalid", Nodes: []Node{node}}
		if Validate(&d) == nil {
			t.Fatalf("accepted %+v", node)
		}
	}
	s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) { return "ok", nil })
	n := skill("a")
	n.RetryMax = 2
	n.RetryBackoffMS = 1
	r := finished(t, s, start(t, s, save(t, s, n)))
	if r.Snapshot.Nodes[0].RetryMax != 2 || r.Snapshot.Nodes[0].RetryBackoffMS != 1 {
		t.Fatal("retry config not frozen")
	}
}
