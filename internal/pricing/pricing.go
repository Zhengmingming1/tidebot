// Package pricing turns token usage into money using per-model prices that the
// user controls (with official presets), instead of Claude Code's
// total_cost_usd, which applies Anthropic's price table to whatever model name
// it sees and is wrong for Kimi / DeepSeek / GLM / Qwen and meaningless for
// flat subscriptions.
//
// Prices are per 1M tokens in the model's currency (USD or CNY). A price that
// is nil is "not set" (distinct from 0 = free).
package pricing

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Billing modes.
const (
	ModeMetered      = "metered"      // 按量计费
	ModeSubscription = "subscription" // 订阅套餐
	ModeFree         = "free"         // 本地免费
)

// Cost sources (how a run's amount was obtained).
const (
	SourcePriced       = "priced"       // tokens × prices, all prices known
	SourceEstimate     = "estimate"     // some price missing or approximated
	SourceClaude       = "claude"       // Claude Code's own figure (official Anthropic only)
	SourceSubscription = "subscription" // flat plan: tokens/requests counted, money 0
	SourceFree         = "free"
	SourceLegacy       = "legacy" // recorded before 0.1.7 from total_cost_usd
)

// Currencies.
const (
	USD = "USD"
	CNY = "CNY"
)

// Tier is a price band by prompt length (input + cache read + cache write of one
// request). UpTo is exclusive; 0 means no upper bound. Tiers are sorted by UpTo.
type Tier struct {
	UpTo       int64    `json:"up_to"`
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
}

// ModelPrice is the price of one model on one provider.
type ModelPrice struct {
	Model      string   `json:"model"` // API model id; "*" suffix matches a prefix
	Currency   string   `json:"currency"`
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	Tiers      []Tier   `json:"tiers,omitempty"`
	Note       string   `json:"note,omitempty"`
	Source     string   `json:"source,omitempty"`  // official pricing page URL
	Fetched    string   `json:"fetched,omitempty"` // YYYY-MM-DD the page was read
}

// Quota is a subscription allowance in a window.
type Quota struct {
	Window      string `json:"window"` // 5h | day | week | month
	MaxTokens   int64  `json:"max_tokens,omitempty"`
	MaxRequests int64  `json:"max_requests,omitempty"`
}

// Billing is how a provider charges.
type Billing struct {
	Mode        string  `json:"mode"`
	Quota       *Quota  `json:"quota,omitempty"`
	MonthlyFee  float64 `json:"monthly_fee,omitempty"`
	FeeCurrency string  `json:"fee_currency,omitempty"`
	Amortize    bool    `json:"amortize,omitempty"` // show monthly fee spread per day on the Spend page
}

// F is a helper for literal prices.
func F(v float64) *float64 { return &v }

// Normalize lowercases a model id and drops Claude Code's "[1m]"-style suffix.
func Normalize(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, '['); i > 0 {
		m = m[:i]
	}
	return m
}

// Find returns the price entry for a model: exact match first, then the
// longest "prefix*" pattern.
func Find(prices []ModelPrice, model string) *ModelPrice {
	m := Normalize(model)
	if m == "" {
		return nil
	}
	var best *ModelPrice
	bestLen := -1
	for i := range prices {
		k := Normalize(prices[i].Model)
		if k == m {
			return &prices[i]
		}
		if strings.HasSuffix(k, "*") && strings.HasPrefix(m, strings.TrimSuffix(k, "*")) && len(k) > bestLen {
			best, bestLen = &prices[i], len(k)
		}
	}
	return best
}

// Rates are the four per-1M prices for one request.
type Rates struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
	TierUpTo   int64   `json:"tier_up_to"`        // band used (0 = base / unbounded)
	Missing    bool    `json:"missing,omitempty"` // input or output price not set
	Approx     bool    `json:"approx,omitempty"`  // cache price not set, input price used instead
}

// RatesFor picks the band for a prompt length (prompt < 0: base / first band).
func (p ModelPrice) RatesFor(prompt int64) Rates {
	in, out, cw, cr := p.Input, p.Output, p.CacheWrite, p.CacheRead
	r := Rates{}
	if len(p.Tiers) > 0 {
		t := p.Tiers[0]
		if prompt >= 0 {
			for _, x := range p.Tiers {
				t = x
				if x.UpTo == 0 || prompt < x.UpTo {
					break
				}
			}
		}
		r.TierUpTo = t.UpTo
		in, out = t.Input, t.Output
		if t.CacheWrite != nil {
			cw = t.CacheWrite
		}
		if t.CacheRead != nil {
			cr = t.CacheRead
		}
	}
	if in == nil || out == nil {
		r.Missing = true
		if in != nil {
			r.Input = *in
		}
		if out != nil {
			r.Output = *out
		}
		return r
	}
	r.Input, r.Output = *in, *out
	if cw != nil {
		r.CacheWrite = *cw
	} else {
		r.CacheWrite, r.Approx = *in, true
	}
	if cr != nil {
		r.CacheRead = *cr
	} else {
		r.CacheRead, r.Approx = *in, true
	}
	return r
}

// Usage is token usage of one API request or an aggregate.
type Usage struct {
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
	Requests   int64  `json:"requests"`
}

// Prompt is the prompt length that selects a tier.
func (u Usage) Prompt() int64 { return u.Input + u.CacheRead + u.CacheWrite }

// Total is every token counted.
func (u Usage) Total() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

func (u *Usage) add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Requests += o.Requests
}

// Amount is usage × rates (per 1M).
func (r Rates) Amount(u Usage) float64 {
	return (float64(u.Input)*r.Input + float64(u.Output)*r.Output + float64(u.CacheWrite)*r.CacheWrite + float64(u.CacheRead)*r.CacheRead) / 1e6
}

// Line is one model's share of a run, with the calculation shown in the UI.
type Line struct {
	Usage
	Currency string  `json:"currency"`
	Rates    *Rates  `json:"rates,omitempty"`
	Amount   float64 `json:"amount"`
	Missing  bool    `json:"missing,omitempty"`
	Approx   bool    `json:"approx,omitempty"`
	Tiered   bool    `json:"tiered,omitempty"` // several bands were used
}

// Cost is the money side of a run.
type Cost struct {
	Amount   float64      `json:"amount"`
	Currency string       `json:"currency"`
	Source   string       `json:"source"`
	Mode     string       `json:"mode"`
	Totals   Usage        `json:"totals"`
	Lines    []Line       `json:"lines"`
	Snapshot []ModelPrice `json:"snapshot,omitempty"` // prices as used, frozen with the run
}

// Validate checks user-entered prices and billing.
func Validate(b Billing, prices []ModelPrice) error {
	switch b.Mode {
	case "", ModeMetered, ModeFree:
	case ModeSubscription:
		if q := b.Quota; q != nil {
			switch q.Window {
			case "5h", "day", "week", "month":
			default:
				return errors.New("quota window must be 5h, day, week or month")
			}
			if q.MaxTokens < 0 || q.MaxRequests < 0 {
				return errors.New("quota must not be negative")
			}
		}
	default:
		return errors.New("billing mode must be metered, subscription or free")
	}
	if b.MonthlyFee < 0 || math.IsNaN(b.MonthlyFee) {
		return errors.New("monthly fee must not be negative")
	}
	if b.FeeCurrency != "" && b.FeeCurrency != USD && b.FeeCurrency != CNY {
		return errors.New("currency must be USD or CNY")
	}
	seen := map[string]bool{}
	for _, p := range prices {
		k := Normalize(p.Model)
		if k == "" {
			return errors.New("price needs a model id")
		}
		if seen[k] {
			return fmt.Errorf("model %s listed twice", p.Model)
		}
		seen[k] = true
		if p.Currency != USD && p.Currency != CNY {
			return fmt.Errorf("%s: currency must be USD or CNY", p.Model)
		}
		for _, v := range []*float64{p.Input, p.Output, p.CacheWrite, p.CacheRead} {
			if v != nil && (*v < 0 || *v > 10000 || math.IsNaN(*v)) {
				return fmt.Errorf("%s: price out of range", p.Model)
			}
		}
		last := int64(-1)
		for i, t := range p.Tiers {
			if t.UpTo == 0 && i != len(p.Tiers)-1 {
				return fmt.Errorf("%s: only the last tier may be unbounded", p.Model)
			}
			if t.UpTo != 0 && t.UpTo <= last {
				return fmt.Errorf("%s: tiers must be in increasing order", p.Model)
			}
			last = t.UpTo
			for _, v := range []*float64{t.Input, t.Output, t.CacheWrite, t.CacheRead} {
				if v != nil && (*v < 0 || *v > 10000) {
					return fmt.Errorf("%s: price out of range", p.Model)
				}
			}
		}
	}
	return nil
}

// Calculator prices a run. Requests are per-API-call usages (exact tiers);
// Totals are Claude Code's per-model aggregates from the result event, which
// also include anything not seen as a separate request.
type Calculator struct {
	Billing  Billing
	Prices   []ModelPrice
	Fallback string // model to bill when the stream names none (the profile's model)
	// Official is true for Anthropic's own API: if a price is missing there,
	// Claude Code's figure (Anthropic's table) is a sound fallback.
	Official bool
}

// Compute returns the run cost.
func (c Calculator) Compute(requests []Usage, totals map[string]Usage, claudeUSD float64) Cost {
	mode := c.Billing.Mode
	if mode == "" {
		mode = ModeMetered
	}
	byModel := map[string][]Usage{}
	reqTotals := map[string]Usage{}
	for _, r := range requests {
		m := Normalize(r.Model)
		if m == "" {
			m = Normalize(c.Fallback)
		}
		r.Model = m
		if r.Requests == 0 {
			r.Requests = 1
		}
		byModel[m] = append(byModel[m], r)
		t := reqTotals[m]
		t.Model = m
		t.add(r)
		reqTotals[m] = t
	}
	agg := map[string]Usage{}
	for k, u := range totals {
		m := Normalize(k)
		if m == "" {
			m = Normalize(c.Fallback)
		}
		u.Model = m
		x := agg[m]
		x.Model = m
		x.add(u)
		agg[m] = x
	}
	// Per model: use the request list when it covers the aggregate (≥ 95% of
	// tokens), else the aggregate priced at the base band.
	models := map[string]bool{}
	for m := range byModel {
		models[m] = true
	}
	for m := range agg {
		models[m] = true
	}
	names := make([]string, 0, len(models))
	for m := range models {
		names = append(names, m)
	}
	sort.Strings(names)

	out := Cost{Mode: mode, Lines: []Line{}}
	used := map[string]ModelPrice{}
	missing, approx := false, false
	currency := ""
	for _, m := range names {
		reqs, a := byModel[m], agg[m]
		rt := reqTotals[m]
		useReqs := len(reqs) > 0 && (a.Total() == 0 || float64(rt.Total()) >= 0.95*float64(a.Total()))
		line := Line{}
		if useReqs {
			line.Usage = rt
			if a.Requests > rt.Requests {
				line.Requests = a.Requests
			}
		} else {
			line.Usage = a
			if line.Requests == 0 {
				line.Requests = rt.Requests
			}
		}
		line.Model = m
		p := Find(c.Prices, m)
		if mode != ModeMetered || p == nil {
			line.Missing = mode == ModeMetered
			missing = missing || line.Missing
			out.Lines = append(out.Lines, line)
			out.Totals.add(line.Usage)
			continue
		}
		used[Normalize(p.Model)] = *p
		line.Currency = p.Currency
		if currency == "" {
			currency = p.Currency
		}
		if useReqs && len(p.Tiers) > 0 {
			bands := map[int64]bool{}
			var last Rates
			for _, r := range reqs {
				rr := p.RatesFor(r.Prompt())
				bands[rr.TierUpTo] = true
				line.Amount += rr.Amount(r)
				line.Missing = line.Missing || rr.Missing
				line.Approx = line.Approx || (rr.Approx && r.CacheRead+r.CacheWrite > 0)
				last = rr
			}
			line.Tiered = len(bands) > 1
			line.Rates = &last
		} else {
			rr := p.RatesFor(-1)
			if len(p.Tiers) > 1 {
				line.Approx = true // aggregate only: can't tell the band per request
			}
			line.Amount = rr.Amount(line.Usage)
			line.Missing, line.Approx = rr.Missing, line.Approx || (rr.Approx && line.CacheRead+line.CacheWrite > 0)
			line.Rates = &rr
		}
		if p.Currency != currency {
			line.Approx = true // mixed currencies are converted by the caller; flag it
		}
		missing = missing || line.Missing
		approx = approx || line.Approx
		out.Lines = append(out.Lines, line)
		out.Totals.add(line.Usage)
	}
	for _, p := range used {
		out.Snapshot = append(out.Snapshot, p)
	}
	sort.Slice(out.Snapshot, func(i, j int) bool { return out.Snapshot[i].Model < out.Snapshot[j].Model })
	if currency == "" {
		currency = USD
		for _, p := range c.Prices {
			currency = p.Currency
			break
		}
	}
	out.Currency = currency
	switch mode {
	case ModeFree:
		out.Source = SourceFree
		return out
	case ModeSubscription:
		out.Source = SourceSubscription
		return out
	}
	for _, l := range out.Lines {
		if l.Currency == currency {
			out.Amount += l.Amount
		}
	}
	switch {
	case missing && c.Official && claudeUSD > 0:
		out.Amount, out.Currency, out.Source = claudeUSD, USD, SourceClaude
	case missing || approx:
		out.Source = SourceEstimate
	default:
		out.Source = SourcePriced
	}
	return out
}

// Meter accumulates per-request usage during a run (for mid-run budget checks).
// Claude Code emits one assistant event per content block, all carrying the
// message's usage, so usage is keyed by message id and the largest values win.
type Meter struct {
	Calc  Calculator
	byID  map[string]Usage
	order []string
}

// NewMeter returns a meter for a calculator.
func NewMeter(c Calculator) *Meter { return &Meter{Calc: c, byID: map[string]Usage{}} }

// Add records the usage of one request (msgID may repeat).
func (m *Meter) Add(msgID string, u Usage) {
	if msgID == "" {
		msgID = fmt.Sprintf("anon-%d", len(m.order))
	}
	old, ok := m.byID[msgID]
	if !ok {
		m.order = append(m.order, msgID)
		old = u
	}
	old.Model = u.Model
	old.Input = max(old.Input, u.Input)
	old.Output = max(old.Output, u.Output)
	old.CacheRead = max(old.CacheRead, u.CacheRead)
	old.CacheWrite = max(old.CacheWrite, u.CacheWrite)
	old.Requests = 1
	m.byID[msgID] = old
}

// Requests returns the per-request usages in arrival order.
func (m *Meter) Requests() []Usage {
	out := make([]Usage, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.byID[id])
	}
	return out
}

// Now prices what has been seen so far.
func (m *Meter) Now() Cost { return m.Calc.Compute(m.Requests(), nil, 0) }
