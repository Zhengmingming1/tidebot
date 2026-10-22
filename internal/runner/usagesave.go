package runner

import (
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// DefaultUsageSaveEvery is how often a running run's token usage and priced
// cost are written to the database (Runner.UsageSaveEvery overrides it).
const DefaultUsageSaveEvery = 5 * time.Second

// usageSaver saves a running run's usage so a crash, force quit or power loss
// keeps what was already spent (spend page, budgets, run detail) instead of $0.
//
// It behaves like a throttle with leading and trailing edges: the first change
// is saved at once, later changes at most once per interval, and the last
// change of a burst is saved when the interval ends. snap returns the current
// cost and whether it changed since the previous snapshot; save does the write.
// Both run on the saver's own goroutine, so a slow database never blocks
// reading Claude Code's output. The final write after the run stays with the
// caller: stop waits for an in-flight save, so it can't land after it.
type usageSaver struct {
	every time.Duration
	snap  func() (pricing.Cost, bool)
	save  func(pricing.Cost)
	kick  chan struct{}
	quit  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func startUsageSaver(every time.Duration, snap func() (pricing.Cost, bool), save func(pricing.Cost)) *usageSaver {
	s := &usageSaver{every: every, snap: snap, save: save,
		kick: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	go s.loop()
	return s
}

// changed tells the saver there is new usage. It never blocks.
func (s *usageSaver) changed() {
	if s == nil {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// stop ends the saver and waits for a save in progress. Safe to call twice.
func (s *usageSaver) stop() {
	if s == nil {
		return
	}
	s.once.Do(func() { close(s.quit) })
	<-s.done
}

func (s *usageSaver) loop() {
	defer close(s.done)
	var (
		last    time.Time // last save
		timer   *time.Timer
		timerC  <-chan time.Time
		pending bool
	)
	flush := func() {
		pending = false
		last = time.Now()
		if c, ok := s.snap(); ok {
			s.save(c)
		}
	}
	for {
		select {
		case <-s.quit:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.kick:
			if pending {
				continue // the trailing save is already scheduled
			}
			if wait := s.every - time.Since(last); wait > 0 {
				pending = true
				if timer == nil {
					timer = time.NewTimer(wait)
				} else {
					timer.Reset(wait)
				}
				timerC = timer.C
				continue
			}
			flush()
		case <-timerC:
			timerC = nil
			flush()
		}
	}
}

// usageChanged reports whether two cost totals differ (tokens or requests).
func usageChanged(a, b pricing.Usage) bool {
	return a.Input != b.Input || a.Output != b.Output || a.CacheRead != b.CacheRead ||
		a.CacheWrite != b.CacheWrite || a.Requests != b.Requests
}
