package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"sync"
	"time"
)

type taskPolicyLock struct {
	mu   sync.Mutex
	refs int
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
