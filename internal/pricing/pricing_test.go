package pricing

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestFindExactAndGlob(t *testing.T) {
	ps := []ModelPrice{{Model: "claude-opus-5*"}, {Model: "claude-opus-5-5*"}, {Model: "kimi-k3"}}
	if p := Find(ps, "claude-opus-5-5-20260901"); p == nil || p.Model != "claude-opus-5-5*" {
		t.Fatalf("longest glob: %+v", p)
	}
	if p := Find(ps, "claude-opus-5-20260101"); p == nil || p.Model != "claude-opus-5*" {
		t.Fatalf("glob: %+v", p)
	}
	if p := Find(ps, "Kimi-K3[1m]"); p == nil || p.Model != "kimi-k3" {
		t.Fatalf("normalize: %+v", p)
	}
	if Find(ps, "kimi-k3-turbo") != nil {
		t.Fatal("exact names must not prefix-match")
	}
}

func TestTiersPerRequest(t *testing.T) {
	p := ModelPrice{Model: "glm-5.1", Currency: CNY, Input: F(6), Output: F(24), Tiers: []Tier{
		{UpTo: 32000, Input: F(6), Output: F(24), CacheRead: F(1.3), CacheWrite: F(6)},
		{Input: F(8), Output: F(28), CacheRead: F(2), CacheWrite: F(8)},
	}}
	c := Calculator{Prices: []ModelPrice{p}}
	reqs := []Usage{{Model: "glm-5.1", Input: 10000, Output: 1000}, {Model: "glm-5.1", Input: 40000, Output: 1000}}
	got := c.Compute(reqs, nil, 0)
	want := (10000*6.0+1000*24)/1e6 + (40000*8.0+1000*28)/1e6
	if !near(got.Amount, want) || got.Source != SourcePriced || !got.Lines[0].Tiered {
		t.Fatalf("%+v want %v", got, want)
	}
	// aggregate only → base band, marked estimate
	agg := c.Compute(nil, map[string]Usage{"glm-5.1": {Input: 50000, Output: 2000}}, 0)
	if agg.Source != SourceEstimate {
		t.Fatalf("aggregate should be an estimate: %+v", agg)
	}
}

func TestComputeSources(t *testing.T) {
	ps := []ModelPrice{{Model: "kimi-k3", Currency: USD, Input: F(3), Output: F(15), CacheRead: F(0.3), CacheWrite: F(3)}}
	u := []Usage{{Model: "kimi-k3", Input: 1e6, Output: 1e6, CacheRead: 1e6}}
	if c := (Calculator{Prices: ps}).Compute(u, nil, 0); c.Source != SourcePriced || !near(c.Amount, 18.3) || len(c.Snapshot) != 1 {
		t.Fatalf("priced %+v", c)
	}
	if c := (Calculator{Prices: ps}).Compute([]Usage{{Model: "other", Input: 10}}, nil, 0); c.Source != SourceEstimate || !c.Lines[0].Missing {
		t.Fatalf("missing price %+v", c)
	}
	if c := (Calculator{Official: true}).Compute([]Usage{{Model: "claude-x", Input: 10}}, nil, 0.42); c.Source != SourceClaude || c.Amount != 0.42 {
		t.Fatalf("claude fallback %+v", c)
	}
	if c := (Calculator{Billing: Billing{Mode: ModeSubscription}, Prices: ps}).Compute(u, nil, 3); c.Source != SourceSubscription || c.Amount != 0 || c.Totals.Total() != 3e6 {
		t.Fatalf("subscription %+v", c)
	}
	if c := (Calculator{Billing: Billing{Mode: ModeFree}}).Compute(u, nil, 3); c.Source != SourceFree || c.Amount != 0 {
		t.Fatalf("free %+v", c)
	}
	// missing cache price falls back to input price and is flagged only when cache was used
	noCache := []ModelPrice{{Model: "q", Currency: CNY, Input: F(2), Output: F(8)}}
	if c := (Calculator{Prices: noCache}).Compute([]Usage{{Model: "q", Input: 100}}, nil, 0); c.Source != SourcePriced {
		t.Fatalf("%+v", c)
	}
	if c := (Calculator{Prices: noCache}).Compute([]Usage{{Model: "q", Input: 100, CacheRead: 100}}, nil, 0); c.Source != SourceEstimate {
		t.Fatalf("%+v", c)
	}
}

func TestMeterDedupe(t *testing.T) {
	m := NewMeter(Calculator{})
	m.Add("a", Usage{Model: "x", Input: 100})
	m.Add("a", Usage{Model: "x", Input: 100, Output: 40})
	m.Add("b", Usage{Model: "x", Input: 5, Output: 1})
	r := m.Requests()
	if len(r) != 2 || r[0].Output != 40 || r[0].Input != 100 {
		t.Fatalf("%+v", r)
	}
}

func TestFX(t *testing.T) {
	f := FX{Display: CNY, USDCNY: 7}
	if !near(f.Convert(1, USD, CNY), 7) || !near(f.Convert(7, CNY, USD), 1) || f.Convert(3, CNY, CNY) != 3 {
		t.Fatal("convert")
	}
	if (FX{Display: "EUR", USDCNY: 7}).Validate() == nil || (FX{Display: USD, USDCNY: 0}).Validate() == nil {
		t.Fatal("validate")
	}
	c := Cost{Source: SourcePriced, Lines: []Line{{Currency: USD, Amount: 1}, {Currency: CNY, Amount: 7}}}
	if !near(c.Total(f, USD), 2) {
		t.Fatal("mixed total")
	}
}

func TestValidate(t *testing.T) {
	if Validate(Billing{Mode: "bogus"}, nil) == nil {
		t.Fatal("mode")
	}
	if Validate(Billing{Mode: ModeMetered}, []ModelPrice{{Model: "x", Currency: USD, Input: F(-1)}}) == nil {
		t.Fatal("negative price")
	}
	if err := Validate(Billing{Mode: ModeSubscription, Quota: &Quota{Window: "5h", MaxTokens: 1000}}, nil); err != nil {
		t.Fatal(err)
	}
}
