package app

import (
	"context"
	"sync"
)

// Event kinds delivered to in-process subscribers (the desktop shell's notifications).
const (
	EventRunFinished    = "run_finished"
	EventRunFailed      = "run_failed"
	EventApprovalNeeded = "approval_needed"
	EventBudgetBreached = "budget_breached"
)

// Event is a user-facing occurrence. It never carries secrets or prompt bodies.
type Event struct {
	Kind     string
	TaskID   string
	TaskName string
	RunID    string
	Detail   string
	Count    int
	CostUSD  float64
}

// Bus is a tiny non-blocking fan-out. A nil *Bus is valid and drops everything.
type Bus struct {
	mu   sync.Mutex
	subs []chan Event
}

// Subscribe returns a buffered channel; slow consumers lose events rather than block runs.
func (b *Bus) Subscribe(buf int) <-chan Event {
	if buf <= 0 {
		buf = 16
	}
	ch := make(chan Event, buf)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Emit delivers e to every subscriber without blocking.
func (b *Bus) Emit(e Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (a *App) taskName(id string) string {
	if id == "" || a.Store == nil {
		return ""
	}
	t, err := a.Store.GetTask(context.Background(), id)
	if err != nil {
		return ""
	}
	return t.Name
}
