package webui

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/robfig/cron/v3"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/pricing/catalog"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
)

// BudgetGuess is the new-task form's daily budget suggestion (tasks are capped per
// day): a per-run figure × the most runs the schedule fires in one day. Amounts are in
// the display currency. Basis says where the per-run figure came from:
//
//	similar      – past runs of similar tasks on this provider (P90 × 1.5)
//	provider     – past runs of any task on this provider (P75 × 1.5)
//	price        – the model's prices × a typical agent run (× 3 headroom)
//	subscription – flat plan: runs cost nothing extra, so no cap
//	free         – local / free provider
//	none         – nothing to go on (no price known)
type BudgetGuess struct {
	Amount   float64  `json:"amount"`
	Currency string   `json:"currency"`
	Basis    string   `json:"basis"`
	Runs     int      `json:"runs,omitempty"`
	P90      float64  `json:"p90,omitempty"`
	Max      float64  `json:"max,omitempty"`
	Estimate float64  `json:"estimate,omitempty"`
	Model    string   `json:"model,omitempty"`
	Similar  []string `json:"similar,omitempty"`
	Heavy    bool     `json:"heavy,omitempty"`
	PerRun   float64  `json:"per_run,omitempty"` // suggested per-run allowance before × runs/day
	PerDay   int      `json:"per_day"`           // max runs in one day under the schedule
}

// runsPerDay is the most times cron fires in one calendar day over the next 14 days
// (manual / invalid → 1, capped at 288).
func runsPerDay(expr string, loc *time.Location) int {
	if strings.TrimSpace(expr) == "" {
		return 1
	}
	sch, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor).Parse(expr)
	if err != nil {
		return 1
	}
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	end := now.AddDate(0, 0, 14)
	per := map[string]int{}
	best := 0
	for t, i := scheduler.NextIn(sch, now, loc), 0; !t.IsZero() && t.Before(end) && i < 5000; t, i = scheduler.NextIn(sch, t, loc), i+1 {
		d := t.Format("2006-01-02")
		per[d]++
		if per[d] > best {
			best = per[d]
		}
	}
	if best == 0 {
		return 1
	}
	if best > 288 {
		best = 288
	}
	return best
}

// finish turns a per-run allowance into the daily cap.
func (g *BudgetGuess) finish(perRun float64) *BudgetGuess {
	g.PerRun = budgetRound(perRun)
	g.Amount = budgetRound(perRun * float64(g.PerDay))
	return g
}

// typicalRun is a mid-sized Claude Code run: 25 requests of ~30k prompt tokens,
// mostly served from cache, ~1k output each.
func typicalRun(model string) []pricing.Usage {
	out := make([]pricing.Usage, 25)
	for i := range out {
		out[i] = pricing.Usage{Model: model, Input: 2400, CacheRead: 24000, CacheWrite: 3600, Output: 1000, Requests: 1}
	}
	return out
}

var heavyWords = []string{"重构", "迁移", "实现", "开发", "全部仓库", "所有仓库", "整个项目", "写测试", "修复所有", "refactor", "migrate", "implement", "rewrite", "all repos", "every repo", "whole codebase", "write tests"}

// isHeavy: instructions that usually mean long, tool-heavy runs.
func isHeavy(text string) bool {
	t := strings.ToLower(text)
	for _, w := range heavyWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// budgetRound: a nice number, at least 0.05, on the form's 0.01 step.
func budgetRound(v float64) float64 {
	a := math.Ceil(niceUp(v)*100-1e-9) / 100
	if a < 0.05 {
		a = 0.05
	}
	return a
}

// niceUp rounds up to 1 / 2 / 2.5 / 5 × 10^k.
func niceUp(v float64) float64 {
	if v <= 0 {
		return 0
	}
	e := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if c := m * e; c >= v-1e-12 {
			return math.Round(c*10000) / 10000
		}
	}
	return 10 * e
}

func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := int(math.Ceil(q*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	return s[i]
}

// terms: lower-cased latin words (≥3 letters) and CJK bigrams, for a cheap similarity.
func terms(s string) map[string]bool {
	out := map[string]bool{}
	var word []rune
	var prev rune
	flush := func() {
		if len(word) >= 3 {
			out[string(word)] = true
		}
		word = word[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 0x3400 && unicode.Is(unicode.Han, r):
			flush()
			if prev != 0 {
				out[string([]rune{prev, r})] = true
			}
			prev = r
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)
		default:
			flush()
		}
		prev = 0
	}
	flush()
	return out
}

// similarity is the overlap coefficient |A∩B| / min(|A|,|B|).
func similarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	m := len(a)
	if len(b) < m {
		m = len(b)
	}
	return float64(n) / float64(m)
}

type pastRun struct {
	task, name, prompt string
	amt                float64
}

// suggestBudget looks at history first, then at prices.
func (s *Server) suggestBudget(ctx context.Context, name, prompt, providerName, cronExpr string) *BudgetGuess {
	f := s.fx()
	g := &BudgetGuess{Currency: f.Display, Heavy: isHeavy(name + " " + prompt), PerDay: runsPerDay(cronExpr, s.deps.Location)}
	if s.deps.Reg == nil || providerName == "" {
		g.Basis = "none"
		return g
	}
	p, ok := s.deps.Reg.Get(providerName)
	if !ok {
		g.Basis = "none"
		return g
	}
	g.Model = p.Model
	ep := p.EffectivePricing()
	switch ep.Billing.Mode {
	case pricing.ModeSubscription:
		g.Basis = "subscription"
		return g
	case pricing.ModeFree:
		g.Basis = "free"
		return g
	}

	// History: finished, priced runs on this provider in the last 120 days.
	var past []pastRun
	if s.deps.DB != nil {
		rows, err := s.deps.DB.QueryContext(ctx, `SELECT r.task_id, t.name, t.prompt, `+s.runAmt("r.")+` FROM runs r JOIN tasks t ON t.id = r.task_id
			WHERE r.provider_name = ? AND r.status IN ('succeeded','failed') AND r.cost_source NOT IN ('subscription','free')
			  AND r.created_at >= datetime('now','-120 days') ORDER BY r.created_at DESC LIMIT 600`, providerName)
		if err == nil {
			for rows.Next() {
				var x pastRun
				if rows.Scan(&x.task, &x.name, &x.prompt, &x.amt) == nil && x.amt > 0 {
					past = append(past, x)
				}
			}
			rows.Close()
		}
	}
	want := terms(name + " " + prompt)
	var sim, all []float64
	simTask := map[string]float64{}
	names := map[string]string{}
	for _, x := range past {
		all = append(all, x.amt)
		sc, seen := simTask[x.task]
		if !seen {
			sc = math.Max(similarity(want, terms(x.name+" "+x.prompt)), similarity(want, terms(x.name)))
			simTask[x.task], names[x.task] = sc, x.name
		}
		if sc >= 0.34 {
			sim = append(sim, x.amt)
		}
	}
	if len(sim) >= 3 {
		g.Basis, g.Runs, g.P90, g.Max = "similar", len(sim), quantile(sim, 0.9), quantile(sim, 1)
		g.finish(g.P90 * 1.5)
		for id, sc := range simTask {
			if sc >= 0.34 && len(g.Similar) < 3 {
				g.Similar = append(g.Similar, names[id])
			}
		}
		sort.Strings(g.Similar)
		return g
	}
	if len(all) >= 5 {
		g.Basis, g.Runs, g.P90, g.Max = "provider", len(all), quantile(all, 0.75), quantile(all, 1)
		if g.Heavy {
			return g.finish(g.P90 * 3)
		}
		return g.finish(g.P90 * 1.5)
	}

	// Prices × a typical run.
	calc := pricing.Calculator{Billing: ep.Billing, Prices: ep.Prices, Fallback: p.Model, Lookup: catalog.Active().Lookup}
	c := calc.Compute(typicalRun(p.Model), nil, 0)
	est := c.Total(f, f.Display)
	if est <= 0 {
		g.Basis = "none"
		return g
	}
	g.Basis, g.Estimate = "price", est
	mult := 3.0
	if g.Heavy {
		mult = 6
	}
	return g.finish(est * mult)
}
