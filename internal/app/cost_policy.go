package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// taskCostMode is an operational setting, not part of immutable prompt versions.
func (a *App) taskCostMode(ctx context.Context, taskID string) (cost.Mode, int64, error) {
	if taskID == "" {
		return cost.ModeTask, 0, nil
	}
	t, err := a.Store.GetTask(ctx, taskID)
	if err != nil {
		return "", 0, err
	}
	mode := cost.Mode(t.CostControlMode)
	if mode == "" {
		mode = cost.ModeTask
	}
	switch mode {
	case cost.ModeTask, cost.ModeGlobal, cost.ModeBypass:
	default:
		return "", 0, fmt.Errorf("invalid cost control mode %q", mode)
	}
	return mode, t.CostPolicyRevision, nil
}

func minPositiveInt(a, b int64) int64 {
	if a <= 0 || (b > 0 && b < a) {
		return b
	}
	return a
}
func minPositiveAmount(a, b float64) float64 {
	if a <= 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

// quotaReservation is independent of task/global budgets. A bypass mode never
// grants permission to exceed a provider subscription's own quota.
type quotaReservation struct {
	Provider, Window                             string
	Since                                        time.Time
	Tokens, Requests, SpentTokens, SpentRequests int64
}

// reserveProviderQuota is called under the shared policy lock at actual launch.
func (a *App) reserveProviderQuota(ctx context.Context, o *runner.Options, p provider.Profile) error {
	b := p.EffectivePricing().Billing
	if b.Mode != pricing.ModeSubscription || b.Quota == nil || a.Cost == nil {
		return nil
	}
	q := b.Quota
	usedT, usedR, since, err := a.Cost.QuotaUsage(ctx, p.Name, q.Window)
	if err != nil {
		return err
	}
	var heldT, heldR int64
	for _, h := range a.quotaHolds {
		if h.Provider == p.Name && h.Window == q.Window && (q.Window == "5h" || h.Since.Equal(since)) {
			if h.Tokens > h.SpentTokens {
				heldT += h.Tokens - h.SpentTokens
			}
			if h.Requests > h.SpentRequests {
				heldR += h.Requests - h.SpentRequests
			}
		}
	}
	h := &quotaReservation{Provider: p.Name, Window: q.Window, Since: since}
	if q.MaxTokens > 0 {
		h.Tokens = q.MaxTokens - usedT - heldT
		if h.Tokens <= 0 {
			return fmt.Errorf("provider quota held or exhausted: %s tokens", p.Name)
		}
		o.Cost.MaxTokens = minPositiveInt(o.Cost.MaxTokens, h.Tokens)
		h.Tokens = o.Cost.MaxTokens
	}
	if q.MaxRequests > 0 {
		h.Requests = q.MaxRequests - usedR - heldR
		if h.Requests <= 0 {
			return fmt.Errorf("provider quota held or exhausted: %s requests", p.Name)
		}
		o.Cost.MaxRequests = minPositiveInt(o.Cost.MaxRequests, h.Requests)
		h.Requests = o.Cost.MaxRequests
	}
	if a.quotaHolds == nil {
		a.quotaHolds = make(map[string]*quotaReservation)
	}
	a.quotaHolds[o.RunID] = h
	return nil
}

func (a *App) saveCostSnapshot(ctx context.Context, o *runner.Options, bucket string, at time.Time) error {
	mode, revision, err := a.taskCostMode(ctx, o.TaskID)
	if err != nil {
		return err
	}
	snapshot := struct {
		Mode          cost.Mode  `json:"mode"`
		Revision      int64      `json:"revision"`
		Currency      string     `json:"currency"`
		FX            pricing.FX `json:"fx"`
		MaxAmount     float64    `json:"max_amount"`
		MaxTokens     int64      `json:"max_tokens"`
		MaxRequests   int64      `json:"max_requests"`
		Bucket        string     `json:"bucket"`
		AccountingAt  string     `json:"accounting_at"`
		AccountingDay string     `json:"accounting_day"`
		Timezone      string     `json:"timezone"`
		LimitSources  []string   `json:"limit_sources"`
	}{mode, revision, o.Cost.Currency, o.Cost.FX, o.Cost.MaxAmount, o.Cost.MaxTokens, o.Cost.MaxRequests, bucket, at.UTC().Format(time.RFC3339), at.In(a.Loc()).Format("2006-01-02"), a.Loc().String(), o.Cost.BudgetSources}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = a.Store.DB.ExecContext(ctx, "UPDATE runs SET effective_cost_policy_json=? WHERE id=?", string(raw), o.RunID)
	return err
}
