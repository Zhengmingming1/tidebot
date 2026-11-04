package cost

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// Mode selects Tidebot budget scopes, never provider quotas or accounting.
type Mode string

const (
	ModeTask   Mode = "task"
	ModeGlobal Mode = "global"
	ModeBypass Mode = "bypass"
)

func (m Mode) Valid() bool              { return m == ModeTask || m == ModeGlobal || m == ModeBypass }
func (m Mode) allows(scope string) bool { return m == ModeTask || (m == ModeGlobal && scope != "task") }

// CheckBeforeRunPolicy checks precisely the selected budgets. A nonpositive
// caller cap means no caller limit, not the legacy implicit five-cent default.
func (s *Service) CheckBeforeRunPolicy(ctx context.Context, taskID, bucket string, callerCapUSD float64, mode Mode) (CheckResult, error) {
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	f := s.GetFX()
	return s.checkPolicy(ctx, taskID, bucket, f.Convert(callerCapUSD, pricing.USD, f.Display), f.Display, mode, false, true)
}

func lowerCap(current, limit float64) float64 {
	if limit > 0 && (current <= 0 || limit < current) {
		return limit
	}
	return current
}
func lowerTokens(current, limit int64) int64 {
	if limit > 0 && (current <= 0 || limit < current) {
		return limit
	}
	return current
}

func (s *Service) checkPolicy(ctx context.Context, taskID, bucket string, capAmt float64, cur string, mode Mode, includeHolds bool, enforceMoney bool) (CheckResult, error) {
	f := s.GetFX()
	out := CheckResult{OK: true, Currency: f.Display}
	if !mode.Valid() {
		return out, fmt.Errorf("invalid cost control mode %q", mode)
	}
	if math.IsNaN(capAmt) || math.IsInf(capAmt, 0) || capAmt < 0 {
		return out, fmt.Errorf("invalid run amount")
	}
	if cur == "" {
		cur = pricing.USD
	}
	out.Currency = cur
	out.RunMaxUSD = capAmt
	if !enforceMoney {
		out.RunMaxUSD = 0
	}
	if capAmt > 0 && enforceMoney {
		out.LimitSources = append(out.LimitSources, "caller/run/money")
	}
	if bucket == "" {
		bucket = BucketProduction
	}
	if mode == ModeTask && taskID != "" {
		b, err := s.findBudget(ctx, "task", taskID, "run", "")
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		if err == nil {
			if b.MaxUSD.Valid && enforceMoney {
				if b.MaxUSD.Float64 > 0 {
					out.LimitSources = append(out.LimitSources, "task/run/money")
				}
				out.RunMaxUSD = lowerCap(out.RunMaxUSD, f.Convert(b.MaxUSD.Float64, b.Currency, cur))
			}
			if b.MaxTokens.Valid {
				if b.MaxTokens.Int64 > 0 {
					out.LimitSources = append(out.LimitSources, "task/run/tokens")
				}
				out.RunMaxTokens = lowerTokens(out.RunMaxTokens, b.MaxTokens.Int64)
			}
		}
	}
	for _, c := range dayChecks(taskID, bucket) {
		if !mode.allows(c.scope) {
			continue
		}
		b, err := s.findBudget(ctx, c.scope, c.scopeID, "day", c.buck)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return out, err
		}
		used, tokens, err := s.sumDay(ctx, s.Today(), c.scope, c.scopeID, c.buck, b.Currency)
		if err != nil {
			return out, err
		}
		held, heldTokens, n := 0.0, int64(0), 0
		if includeHolds {
			held, n = s.Held(c.scope, c.scopeID, c.buck, b.Currency)
			heldTokens = s.heldTokens(c.scope, c.scopeID, c.buck)
		}
		breach := func(reason string, reserved bool) (CheckResult, error) {
			out.OK = false
			out.Reason = reason
			out.BreachScope = c.scope
			out.BreachAction = b.Action
			out.Held = reserved
			if reserved {
				out.BreachAction = "skip"
			}
			return out, nil
		}
		if b.MaxUSD.Valid && b.MaxUSD.Float64 > 0 && enforceMoney {
			out.LimitSources = append(out.LimitSources, c.scope+"/day/money")
			if used >= b.MaxUSD.Float64 {
				return breach(fmt.Sprintf("%s/%s daily %s cap reached", c.scope, c.scopeID, b.Currency), false)
			}
			room := b.MaxUSD.Float64 - used - held
			if room <= 0 || (n > 0 && room < f.Convert(0.01, pricing.USD, b.Currency)) {
				return breach(fmt.Sprintf("%s/%s daily %s cap held by %d running (%.4f+%.4f/%.4f)", c.scope, c.scopeID, b.Currency, n, used, held, b.MaxUSD.Float64), true)
			}
			out.RunMaxUSD = lowerCap(out.RunMaxUSD, f.Convert(room, b.Currency, cur))
			if b.WarnAtPct > 0 && used/b.MaxUSD.Float64*100 >= float64(b.WarnAtPct) {
				out.Warn = true
				out.WarnDetail = fmt.Sprintf("%s spend approaching daily cap", c.scope)
			}
		}
		if c.scope == "task" && b.MaxTokens.Valid && b.MaxTokens.Int64 > 0 {
			out.LimitSources = append(out.LimitSources, c.scope+"/day/tokens")
			if tokens >= b.MaxTokens.Int64 {
				return breach(fmt.Sprintf("%s/%s daily token cap reached", c.scope, c.scopeID), false)
			}
			room := b.MaxTokens.Int64 - tokens - heldTokens
			if room <= 0 {
				return breach(fmt.Sprintf("%s/%s token budget held by running tasks", c.scope, c.scopeID), true)
			}
			out.RunMaxTokens = lowerTokens(out.RunMaxTokens, room)
		}
	}
	return out, nil
}

// ReserveRunPolicy atomically checks and reserves the selected daily money and
// token budgets. Unlimited runs return no hold; callers freeze their launch
// timestamp independently. Tokens conservatively reserve the remaining cap.
// Passing metered=false reserves only tokens for free/subscription providers.
func (s *Service) ReserveRunPolicy(ctx context.Context, taskID, bucket string, capAmt float64, cur string, mode Mode, metered ...bool) (*Hold, float64, CheckResult, error) {
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	enforceMoney := len(metered) == 0 || metered[0]
	out, err := s.checkPolicy(ctx, taskID, bucket, capAmt, cur, mode, true, enforceMoney)
	if err != nil || !out.OK {
		return nil, out.RunMaxUSD, out, err
	}
	if out.RunMaxUSD <= 0 && out.RunMaxTokens <= 0 {
		return nil, 0, out, nil
	}
	if bucket == "" {
		bucket = BucketProduction
	}
	hs := &s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.m == nil {
		hs.m = map[uint64]*hold{}
	}
	hs.next++
	at := s.now()
	f := s.GetFX()
	hs.m[hs.next] = &hold{taskID: taskID, bucket: bucket, day: at.In(s.loc()).Format("2006-01-02"), cap: f.Convert(out.RunMaxUSD, out.Currency, pricing.USD), currency: pricing.USD, fx: &f, mode: mode, tokens: out.RunMaxTokens, accountingAt: at}
	return &Hold{s: s, id: hs.next}, out.RunMaxUSD, out, nil
}

func (s *Service) heldTokens(scope, scopeID, bucket string) int64 {
	hs := &s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var n int64
	for _, h := range hs.m {
		if h.day != s.Today() || (h.mode != "" && !h.mode.allows(scope)) || (scope == "task" && h.taskID != scopeID) || (bucket != "" && h.bucket != bucket) {
			continue
		}
		if left := h.tokens - h.spentTokens; left > 0 {
			n += left
		}
	}
	return n
}

// SetUsage is called after durable ledger update; delayed release is conservative.
func (h *Hold) SetUsage(amount float64, cur string, tokens int64) {
	if h == nil || h.s == nil {
		return
	}
	hs := &h.s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if x := hs.m[h.id]; x != nil {
		f := h.s.GetFX()
		if x.fx != nil {
			f = *x.fx
		}
		x.spent = f.Convert(amount, cur, x.currency)
		x.spentTokens = tokens
	}
}
func (h *Hold) AccountingAt() time.Time {
	if h == nil || h.s == nil {
		return time.Time{}
	}
	hs := &h.s.holds
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if x := hs.m[h.id]; x != nil {
		return x.accountingAt
	}
	return time.Time{}
}

// WithBudgetLock serializes a transaction that mutates policy/FX/budget rows
// with admission and accounting. The callback must use DB/Tx directly and must
// not call Service budget, reservation, or ledger methods (which take this lock).
func (s *Service) WithBudgetLock(fn func() error) error {
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	return fn()
}

// RecordEntryHeld moves usage from a reservation into the ledger atomically
// with admission. A persistence failure retains the old reservation.
func (s *Service) RecordEntryHeld(ctx context.Context, e Entry, h *Hold) error {
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	if err := s.recordEntry(ctx, e); err != nil {
		return err
	}
	if h != nil {
		h.SetUsage(e.Amount, e.Currency, e.Input+e.Output+e.CacheRead+e.CacheWrite)
	}
	return nil
}
