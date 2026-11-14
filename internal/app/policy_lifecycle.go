package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"sync"
	"time"
)

type taskPolicyLock struct {
	mu      sync.Mutex
	refs    int
	waiting bool // its holder waits for a busy folder / pending ledger write
}
type policyRunState struct {
	mu      sync.Mutex
	closing bool
	locks   map[string]*taskPolicyLock
	timers  map[*time.Timer]bool
	wg      sync.WaitGroup
}

func (a *App) enterPolicyRun(task db.Task) (func(), bool) {
	s := &a.policyRuns
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, false
	}
	s.wg.Add(1)
	var lock *taskPolicyLock
	if task.OverlapPolicy != "parallel" {
		if s.locks == nil {
			s.locks = map[string]*taskPolicyLock{}
		}
		lock = s.locks[task.ID]
		if lock == nil {
			lock = &taskPolicyLock{}
			s.locks[task.ID] = lock
		}
		if lock.waiting {
			// A run of this task already waits to start (busy folder, ledger
			// write pending). Its run row comes only when it starts, so the
			// scheduler's overlap check can't see it: without this every firing
			// meanwhile queued up here and they all ran one after another once it
			// cleared ("skip" ran N times). One waiting run stands for them.
			s.mu.Unlock()
			s.wg.Done()
			a.Log.Info("skip run: an earlier run of the task waits to start", "task_id", task.ID)
			return nil, false
		}
		lock.refs++
	}
	s.mu.Unlock()
	if lock != nil {
		lock.mu.Lock()
	}
	return func() {
		if lock != nil {
			lock.mu.Unlock()
			s.mu.Lock()
			lock.refs--
			if lock.refs == 0 {
				delete(s.locks, task.ID)
			}
			s.mu.Unlock()
		}
		s.wg.Done()
	}, true
}

// markPolicyWaiting flags the task's run (holding its policy lock) as waiting to
// start; the returned func clears it. Parallel tasks have no lock: every firing
// is its own run there.
func (a *App) markPolicyWaiting(task db.Task) func() {
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[task.ID]
	if task.OverlapPolicy == "parallel" || lock == nil {
		return func() {}
	}
	lock.waiting = true
	return func() {
		s.mu.Lock()
		lock.waiting = false
		s.mu.Unlock()
	}
}

func (a *App) policyRunsClosing() bool {
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}
func (a *App) schedulePolicyRetry(ctx context.Context, delay time.Duration, fn func()) {
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || ctx.Err() != nil {
		return
	}
	if s.timers == nil {
		s.timers = map[*time.Timer]bool{}
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		delete(s.timers, timer)
		closing := s.closing
		s.mu.Unlock()
		if !closing && ctx.Err() == nil {
			fn()
		}
	})
	s.timers[timer] = true
}
func (a *App) stopPolicyRuns() {
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	for timer := range s.timers {
		timer.Stop()
		delete(s.timers, timer)
	}
}
func (a *App) waitPolicyRuns() {
	done := make(chan struct{})
	go func() { a.policyRuns.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(shutdownWriteWait):
		a.Log.Warn("task policy shutdown timed out")
	}
}
