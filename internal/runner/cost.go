package runner

import (
	"encoding/json"
	"fmt"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

// CostControl prices a run from its token usage and enforces caps mid-run.
type CostControl struct {
	Calc pricing.Calculator
	FX   pricing.FX
	// Currency is the currency of MaxAmount (the display currency).
	Currency string
	// MaxAmount stops the run once its priced cost exceeds it (0 = no cap).
	MaxAmount float64
	// MaxTokens / MaxRequests stop the run once exceeded (0 = no cap).
	MaxTokens   int64
	MaxRequests int64
}

// ErrBudget is the error message prefix for runs stopped by CostControl.
const ErrBudget = "budget exceeded"

// exceeded reports why a run must stop, or "".
func (c *CostControl) exceeded(cost pricing.Cost) string {
	if c == nil {
		return ""
	}
	if c.MaxTokens > 0 && cost.Totals.Total() > c.MaxTokens {
		return fmt.Sprintf("%s: %d tokens > %d", ErrBudget, cost.Totals.Total(), c.MaxTokens)
	}
	if c.MaxRequests > 0 && int64(len(cost.Lines)) > 0 && cost.Totals.Requests > c.MaxRequests {
		return fmt.Sprintf("%s: %d requests > %d", ErrBudget, cost.Totals.Requests, c.MaxRequests)
	}
	if c.MaxAmount > 0 && cost.Source != pricing.SourceSubscription && cost.Source != pricing.SourceFree {
		if v := cost.Total(c.FX, c.Currency); v > c.MaxAmount {
			return fmt.Sprintf("%s: %.4f %s > %.4f %s", ErrBudget, v, c.Currency, c.MaxAmount, c.Currency)
		}
	}
	return ""
}

// assistantUsage extracts (message id, usage) from an assistant event.
func assistantUsage(e Event) (string, pricing.Usage, bool) {
	if e.Type != "assistant" {
		return "", pricing.Usage{}, false
	}
	var m struct {
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *Usage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(e.Raw, &m) != nil || m.Message.Usage == nil {
		return "", pricing.Usage{}, false
	}
	u := m.Message.Usage
	return m.Message.ID, pricing.Usage{Model: m.Message.Model, Input: int64(u.InputTokens), Output: int64(u.OutputTokens),
		CacheRead: int64(u.CacheReadInputTokens), CacheWrite: int64(u.CacheCreationInputTokens), Requests: 1}, true
}

// resultTotals reads the result event's per-model usage (modelUsage), falling
// back to its aggregate usage under the given model.
func resultTotals(e Event, model string) map[string]pricing.Usage {
	out := map[string]pricing.Usage{}
	if len(e.ModelUsage) > 0 {
		var mu map[string]struct {
			InputTokens              int64 `json:"inputTokens"`
			OutputTokens             int64 `json:"outputTokens"`
			CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
			CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
		}
		if json.Unmarshal(e.ModelUsage, &mu) == nil {
			for k, v := range mu {
				out[k] = pricing.Usage{Model: k, Input: v.InputTokens, Output: v.OutputTokens, CacheRead: v.CacheReadInputTokens, CacheWrite: v.CacheCreationInputTokens}
			}
		}
	}
	if len(out) == 0 && e.Usage != nil {
		u := e.Usage
		out[model] = pricing.Usage{Model: model, Input: int64(u.InputTokens), Output: int64(u.OutputTokens),
			CacheRead: int64(u.CacheReadInputTokens), CacheWrite: int64(u.CacheCreationInputTokens)}
	}
	return out
}
