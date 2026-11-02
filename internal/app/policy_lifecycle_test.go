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
