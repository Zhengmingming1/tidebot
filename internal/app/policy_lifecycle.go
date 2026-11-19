package app

import (
	"context"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

type taskPolicyLock struct {
	mu      sync.Mutex
	refs    int
	waiting bool // its holder waits for a busy folder / pending ledger write
}

// policyWait is a scheduled run waiting to start, including parallel tasks
// (they have no overlap lock, but the UI still needs to see why they wait).
type policyWait struct {
	reason string
	n      int // how many of this task's runs are waiting (parallel can be > 1)
}

type policyRunState struct {
	mu      sync.Mutex
	closing bool
	locks   map[string]*taskPolicyLock
	waits   map[string]*policyWait
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

// markPolicyWaiting records that a scheduled run of the task is waiting to
// start, and why, so the task page can show it. The returned func clears this
// one wait. Parallel tasks have no overlap lock (every firing is its own run)
// but they are still recorded here; non-parallel tasks also set the lock's
// waiting flag so later firings keep collapsing to one run.
func (a *App) markPolicyWaiting(task db.Task, reason string) func() {
	if reason == "" {
		return func() {}
	}
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waits == nil {
		s.waits = map[string]*policyWait{}
	}
	w := s.waits[task.ID]
	if w == nil {
		w = &policyWait{}
		s.waits[task.ID] = w
	}
	w.n++
	w.reason = reason
	if task.OverlapPolicy != "parallel" {
		if lock := s.locks[task.ID]; lock != nil {
			lock.waiting = true
		}
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if w.n > 0 {
			w.n--
		}
		if w.n == 0 {
			delete(s.waits, task.ID)
			if lock := s.locks[task.ID]; lock != nil {
				lock.waiting = false
			}
		}
	}
}

// setPolicyWaitReason updates why a task already waiting is waiting (a busy
// folder can become a pending ledger write, or the other way around).
func (a *App) setPolicyWaitReason(taskID, reason string) {
	if reason == "" {
		return
	}
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.waits[taskID]; w != nil {
		w.reason = reason
	}
}

// PolicyWait is why a scheduled run of the task is waiting to start (dir_busy
// or accounting_pending). Empty when it is not waiting.
func (a *App) PolicyWait(taskID string) string {
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.waits[taskID]; w != nil {
		return w.reason
	}
	return ""
}

// PolicyWaits is the in-memory map of task IDs currently waiting to start a
// scheduled run. The WebUI reads this for task APIs and the change stream;
// there is no run row yet, so SQLite's total_changes() would miss it.
func (a *App) PolicyWaits() map[string]string {
	s := &a.policyRuns
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waits) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.waits))
	for id, w := range s.waits {
		out[id] = w.reason
	}
	return out
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
