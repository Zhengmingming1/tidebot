package cost

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// Holds keep the per-run cap of every admitted run (running or queued) counted
// against the daily budgets until the run ends. Without them two runs admitted
// at the same time both see the whole remainder as their cap and together can
// spend twice what is left. A hold's unspent part (cap minus what the run has
// spent so far, which the live ledger already counts) is what it adds on top
// of the ledger, so nothing is counted twice.
type holdSet struct {
	mu   sync.Mutex
	next uint64
	m    map[uint64]*hold
}

type hold struct {
	taskID, bucket, day string
	cap, spent          float64 // in currency
	currency            string
}

// Hold is one admitted run's reservation. The zero / nil Hold is a no-op.
type Hold struct {
	s  *Service
	id uint64
}

// Reserve holds amount (in currency) of today's budgets for one run of taskID
// in bucket. Callers serialize "check then reserve" themselves (App.admitRun).
func (s *Service) Reserve(taskID, bucket string, amount float64, currency string) *Hold {
	if s == nil || amount <= 0 {
		return nil
	}
	if bucket == "" {
		bucket = BucketProduction
	}
	if currency == "" {
		currency = pricing.USD
	}
	hs := &s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.m == nil {
		hs.m = map[uint64]*hold{}
	}
	hs.next++
	hs.m[hs.next] = &hold{taskID: taskID, bucket: bucket, day: s.Today(), cap: amount, currency: currency}
	return &Hold{s: s, id: hs.next}
}

// SetSpent records what the run has spent so far (cumulative, any currency).
func (h *Hold) SetSpent(amount float64, currency string) {
	if h == nil || h.s == nil {
		return
	}
	hs := &h.s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if x := hs.m[h.id]; x != nil {
		x.spent = h.s.GetFX().Convert(amount, currency, x.currency)
	}
}

// Release ends the hold. Safe to call more than once.
func (h *Hold) Release() {
	if h == nil || h.s == nil {
		return
	}
	hs := &h.s.holds
	hs.mu.Lock()
	delete(hs.m, h.id)
	hs.mu.Unlock()
}

// Held returns the unspent part of today's holds that count against a budget
// (scope global / bucket / task), in currency cur, and how many runs hold it.
func (s *Service) Held(scope, scopeID, bucket, cur string) (float64, int) {
	if s == nil {
		return 0, 0
	}
	if cur == "" {
		cur = pricing.USD
	}
	f, day := s.GetFX(), s.Today()
	hs := &s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var sum float64
	n := 0
	for _, x := range hs.m {
		if x.day != day {
			continue // a run from before midnight is charged to the day it started
		}
		if scope == "task" && x.taskID != scopeID {
			continue
		}
		if bucket != "" && x.bucket != bucket {
			continue
		}
		if left := x.cap - x.spent; left > 0 {
			sum += f.Convert(left, x.currency, cur)
			n++
		}
	}
	return sum, n
}

// ReserveRun is the last budget step before a run starts. Under one lock (so
// two runs starting together can't both take the same remainder) it looks at
// every daily money budget the run counts against (global, its bucket, its
// task): if the cap is reached it refuses like CheckBeforeRun; if less than a
// cent is left once the runs already going are counted at their caps, it
// refuses with Held set; otherwise it lowers capAmt (in cur) to the room left
// and holds that much until the returned Hold is released.
func (s *Service) ReserveRun(ctx context.Context, taskID, bucket string, capAmt float64, cur string) (*Hold, float64, CheckResult, error) {
	if bucket == "" {
		bucket = BucketProduction
	}
	if cur == "" {
		cur = pricing.USD
	}
	out := CheckResult{OK: true, RunMaxUSD: capAmt, Currency: cur}
	if capAmt <= 0 {
		return nil, capAmt, out, nil
	}
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	f, day := s.GetFX(), s.Today()
	for _, c := range dayChecks(taskID, bucket) {
		b, err := s.findBudget(ctx, c.scope, c.scopeID, "day", c.buck)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, capAmt, out, err
		}
		if !b.MaxUSD.Valid || b.MaxUSD.Float64 <= 0 {
			continue
		}
		used, _, err := s.sumDay(ctx, day, c.scope, c.scopeID, c.buck, b.Currency)
		if err != nil {
			return nil, capAmt, out, err
		}
		out.BreachScope = c.scope
		if used >= b.MaxUSD.Float64 {
			out.OK = false
			out.Reason = fmt.Sprintf("%s/%s daily %s cap reached (%.4f/%.4f)", c.scope, c.scopeID, b.Currency, used, b.MaxUSD.Float64)
			out.BreachAction = b.Action
			return nil, capAmt, out, nil
		}
		held, n := s.Held(c.scope, c.scopeID, c.buck, b.Currency)
		room := b.MaxUSD.Float64 - used - held
		if n > 0 && room < f.Convert(0.01, pricing.USD, b.Currency) {
			out.OK, out.Held = false, true
			out.Reason = fmt.Sprintf("%s/%s daily %s cap held by %d running (%.4f+%.4f/%.4f)", c.scope, c.scopeID, b.Currency, n, used, held, b.MaxUSD.Float64)
			out.BreachAction = "skip"
			return nil, capAmt, out, nil
		}
		if r := f.Convert(room, b.Currency, cur); r < capAmt {
			capAmt = r
		}
	}
	out.BreachScope = ""
	out.RunMaxUSD = capAmt
	return s.Reserve(taskID, bucket, capAmt, cur), capAmt, out, nil
}

type dayCheck struct{ scope, scopeID, buck string }

// dayChecks lists the daily budgets a run of taskID in bucket counts against.
func dayChecks(taskID, bucket string) []dayCheck {
	out := []dayCheck{{"global", "", ""}, {"bucket", bucket, bucket}}
	if taskID != "" {
		out = append(out, dayCheck{"task", taskID, ""})
	}
	return out
}
