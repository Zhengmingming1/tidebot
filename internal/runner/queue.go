package runner

import (
	"context"
	"errors"
	"sync"
)

// ErrStoppedByUser is the cancel cause (and returned error) of a run the user
// stopped from the WebUI or agentctl. Like ErrShuttingDown it is not a failure:
// no retry, no breaker strike, no "run failed" notice.
var ErrStoppedByUser = errors.New("run stopped by user")

// slots is the concurrency limit as a first-come, first-served queue. Unlike a
// buffered channel it knows who is waiting and in which order, so the WebUI can
// show "queued · #2" and a waiting run can be taken out of the line.
type slots struct {
	mu      sync.Mutex
	max     int
	busy    int
	waiting []*slotWaiter
}

type slotWaiter struct {
	id      string
	ready   chan struct{}
	granted bool // set under mu when the slot is handed over
}

// acquire takes a slot for runID, waiting in line while all are busy. It returns
// nil with the slot held, or the reason it gave up (ctx's cause, or errQuit when
// quit closes) without one. onQueued is called once if it has to wait, with the
// number of runs ahead of it.
func (s *slots) acquire(ctx context.Context, quit <-chan struct{}, runID string, onQueued func(ahead int)) error {
	s.mu.Lock()
	if s.busy < s.max && len(s.waiting) == 0 {
		s.busy++
		s.mu.Unlock()
		return nil
	}
	w := &slotWaiter{id: runID, ready: make(chan struct{})}
	s.waiting = append(s.waiting, w)
	ahead := len(s.waiting) - 1
	s.mu.Unlock()
	if onQueued != nil {
		onQueued(ahead)
	}
	var why error
	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		why = context.Cause(ctx)
	case <-quit:
		why = errQuit
	}
	s.mu.Lock()
	if w.granted { // handed a slot at the same moment: pass it on
		s.mu.Unlock()
		s.release()
		return why
	}
	for i, x := range s.waiting {
		if x == w {
			s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return why
}

// release frees a slot, handing it straight to the first run in line.
func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiting) > 0 {
		w := s.waiting[0]
		s.waiting = s.waiting[1:]
		w.granted = true
		close(w.ready)
		return
	}
	s.busy--
}

// position is runID's place in line (1 = next), 0 when it isn't waiting.
func (s *slots) position(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, w := range s.waiting {
		if w.id == runID {
			return i + 1
		}
	}
	return 0
}

var errQuit = errors.New("quit")

// QueuePos reports a queued run's place in line for a concurrency slot
// (1 = starts next), or 0 when the run isn't waiting.
func (r *Runner) QueuePos(runID string) int { return r.slots.position(runID) }
