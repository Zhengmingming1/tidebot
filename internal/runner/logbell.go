package runner

import (
	"io"
	"sync"
)

// logBells lets readers of a run log (the WebUI's live SSE stream) sleep until the
// run writes something, instead of reopening the file and asking the database on a
// short timer. One channel per watched run, closed on the next write and replaced
// by the next waiter. Runs nobody watches cost one map lookup per write.
type logBells struct {
	mu sync.Mutex
	ch map[string]*bell
}

type bell struct {
	c chan struct{}
	n int // waiters holding c
}

// wait returns a channel that is closed after the next write to runID's log, and a
// func to call when the caller stops waiting (drops the entry if nobody else waits).
func (b *logBells) wait(runID string) (<-chan struct{}, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch == nil {
		b.ch = map[string]*bell{}
	}
	e, ok := b.ch[runID]
	if !ok {
		e = &bell{c: make(chan struct{})}
		b.ch[runID] = e
	}
	e.n++
	var once sync.Once
	return e.c, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			e.n--
			if e.n <= 0 && b.ch[runID] == e {
				delete(b.ch, runID)
			}
		})
	}
}

func (b *logBells) ring(runID string) {
	b.mu.Lock()
	if e, ok := b.ch[runID]; ok {
		close(e.c)
		delete(b.ch, runID)
	}
	b.mu.Unlock()
}

// release wakes everyone still waiting once the run has written its last line.
func (b *logBells) release(runID string) { b.ring(runID) }

// bellWriter rings the run's bell after every write that reached the file.
type bellWriter struct {
	w     io.Writer
	runID string
	b     *logBells
}

func (w bellWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.b.ring(w.runID)
	}
	return n, err
}

// LogWriter wraps w so every write to runID's log wakes its LogWait readers.
func (r *Runner) LogWriter(runID string, w io.Writer) io.Writer {
	return bellWriter{w: w, runID: runID, b: &r.bells}
}

// LogWait returns a channel closed as soon as the run's log file grows (or the run
// ends). Take it before reading the file so a write between read and wait is not
// missed. Only this process's writes ring it; readers keep a slow fallback poll.
// Call done when no longer waiting.
func (r *Runner) LogWait(runID string) (ch <-chan struct{}, done func()) {
	return r.bells.wait(runID)
}

// LogWatchers reports how many runs currently have someone waiting (tests, metrics).
func (r *Runner) LogWatchers() int {
	r.bells.mu.Lock()
	defer r.bells.mu.Unlock()
	return len(r.bells.ch)
}
