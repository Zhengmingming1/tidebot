package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"sync/atomic"
	"testing"
	"time"
)

func TestPolicyShutdownCancelsPendingRetries(t *testing.T) {
	a := newTestApp(t)
	var fired atomic.Bool
	a.schedulePolicyRetry(context.Background(), time.Hour, func() { fired.Store(true) })
	a.stopPolicyRuns()
	a.schedulePolicyRetry(context.Background(), 0, func() { fired.Store(true) })
	a.policyRuns.mu.Lock()
	remaining := len(a.policyRuns.timers)
	a.policyRuns.mu.Unlock()
	if remaining != 0 || fired.Load() {
		t.Fatal("retry survived shutdown")
	}
	if release, ok := a.enterPolicyRun(db.Task{ID: "late"}); ok {
		release()
		t.Fatal("accepted task after shutdown")
	}
}
func TestParallelPolicyDoesNotSerializeAndReleasesLocks(t *testing.T) {
	a := newTestApp(t)
	task := db.Task{ID: "same", OverlapPolicy: "parallel"}
	release, ok := a.enterPolicyRun(task)
	if !ok {
		t.Fatal("refused task")
	}
	defer release()
	done := make(chan struct{})
	go func() {
		release, ok := a.enterPolicyRun(task)
		if ok {
			release()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parallel task serialized")
	}
	task.OverlapPolicy = "queue"
	unlock, ok := a.enterPolicyRun(task)
	if !ok {
		t.Fatal("refused task")
	}
	unlock()
	a.policyRuns.mu.Lock()
	locks := len(a.policyRuns.locks)
	a.policyRuns.mu.Unlock()
	if locks != 0 {
		t.Fatal("task locks leaked")
	}
}

func TestPolicyWaitVisibleIncludingParallel(t *testing.T) {
	a := &App{}
	skip := db.Task{ID: "skip", OverlapPolicy: "skip"}
	if release, ok := a.enterPolicyRun(skip); !ok {
		t.Fatal("refused skip task")
	} else {
		defer release()
	}
	if a.PolicyWait("skip") != "" || a.PolicyWaits() != nil {
		t.Fatal("idle task looked as if it was waiting")
	}
	clear := a.markPolicyWaiting(skip, PolicyWaitDirBusy)
	if a.PolicyWait("skip") != PolicyWaitDirBusy {
		t.Fatalf("skip wait %q", a.PolicyWait("skip"))
	}
	a.setPolicyWaitReason("skip", PolicyWaitAccountingPending)
	if a.PolicyWait("skip") != PolicyWaitAccountingPending {
		t.Fatalf("updated wait %q", a.PolicyWait("skip"))
	}
	if got := a.PolicyWaits(); got["skip"] != PolicyWaitAccountingPending || len(got) != 1 {
		t.Fatalf("waits %v", got)
	}
	clear()
	if a.PolicyWait("skip") != "" || a.PolicyWaits() != nil {
		t.Fatal("cleared wait left behind")
	}

	par := db.Task{ID: "par", OverlapPolicy: "parallel"}
	// Parallel tasks have no overlap lock; waiting must still be queryable.
	c1 := a.markPolicyWaiting(par, PolicyWaitDirBusy)
	c2 := a.markPolicyWaiting(par, PolicyWaitAccountingPending)
	if a.PolicyWait("par") != PolicyWaitAccountingPending {
		t.Fatalf("parallel wait %q", a.PolicyWait("par"))
	}
	c1()
	if a.PolicyWait("par") != PolicyWaitAccountingPending {
		t.Fatal("first of two parallel waits hid the other")
	}
	c2()
	if a.PolicyWait("par") != "" {
		t.Fatal("parallel wait left behind")
	}
}

func TestMarkPolicyWaitingEmptyReasonIsNoop(t *testing.T) {
	a := &App{}
	clear := a.markPolicyWaiting(db.Task{ID: "t"}, "")
	clear()
	a.setPolicyWaitReason("t", PolicyWaitDirBusy)
	if a.PolicyWait("t") != "" {
		t.Fatal("empty mark still recorded a wait")
	}
}
