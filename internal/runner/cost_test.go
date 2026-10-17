package runner

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

func TestFixtureUsageDedupe(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	f, err := os.Open(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "streamjson", "kimi-coding-pong.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	calc := pricing.Calculator{Prices: []pricing.ModelPrice{{Model: "k3-256k", Currency: pricing.CNY, Input: pricing.F(20), Output: pricing.F(100), CacheRead: pricing.F(2), CacheWrite: pricing.F(20)}}}
	m := pricing.NewMeter(calc)
	var res *Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		ev, err := ParseEvent(sc.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if id, u, ok := assistantUsage(ev); ok {
			m.Add(id, u)
		}
		if ev.IsResult() {
			e := ev
			res = &e
		}
	}
	if res == nil {
		t.Fatal("no result")
	}
	c := calc.Compute(m.Requests(), resultTotals(*res, "k3-256k"), res.TotalCostUSD)
	if c.Totals.Input != 611 || c.Totals.Output != 43 || c.Totals.Requests != 1 {
		t.Fatalf("totals %+v", c.Totals)
	}
	want := (611*20.0 + 43*100.0) / 1e6
	if c.Currency != pricing.CNY || c.Source != pricing.SourcePriced || abs(c.Amount-want) > 1e-9 {
		t.Fatalf("cost %+v want %v", c, want)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// A fake claude that keeps streaming usage: tidebot must stop it once the
// priced cost crosses the cap, without waiting for the result event.
func TestBudgetKillMidRun(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for i := 0; i < 50; i++ {
		b.WriteString(`echo '{"type":"assistant","message":{"id":"m` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `","model":"glm-5.3","usage":{"input_tokens":100000,"output_tokens":1000}}}'` + "\nsleep 0.1\n")
	}
	b.WriteString(`echo '{"type":"result","subtype":"success","total_cost_usd":9,"usage":{"input_tokens":1,"output_tokens":1}}'` + "\n")
	if err := os.WriteFile(bin, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	r := New(db.NewStore(sqlDB), nil, 1)
	prices := []pricing.ModelPrice{{Model: "glm-5.3", Currency: pricing.CNY, Input: pricing.F(8), Output: pricing.F(28)}}
	start := time.Now()
	res, err := r.RunOnce(context.Background(), Options{
		ClaudeBin: bin, Prompt: "x", WorkspaceDir: dir, Timeout: 20 * time.Second,
		Profile: provider.Profile{Name: "zhipu", BaseURL: "http://127.0.0.1:9", Model: "glm-5.3"},
		Cost:    &CostControl{Calc: pricing.Calculator{Prices: prices}, FX: pricing.DefaultFX, Currency: pricing.CNY, MaxAmount: 2.5},
	})
	if err == nil || !strings.Contains(res.ErrorMessage, ErrBudget) || res.Status != "canceled" {
		t.Fatalf("err=%v res=%+v", err, res.ErrorMessage)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("run was not stopped early")
	}
	// each request costs 0.8 + 0.028 CNY; the 4th crosses 2.5
	if res.Cost.Totals.Requests != 4 || res.Cost.Currency != pricing.CNY {
		t.Fatalf("cost %+v", res.Cost)
	}
	var amt float64
	var cur, src string
	if err := sqlDB.QueryRow(`SELECT cost_amount, cost_currency, cost_source FROM runs WHERE id=?`, res.RunID).Scan(&amt, &cur, &src); err != nil {
		t.Fatal(err)
	}
	if cur != "CNY" || src != pricing.SourcePriced || abs(amt-4*0.828) > 1e-9 {
		t.Fatalf("stored %v %s %s", amt, cur, src)
	}
}

func TestTokenQuotaKill(t *testing.T) {
	cc := &CostControl{MaxTokens: 1000}
	c := pricing.Cost{Totals: pricing.Usage{Input: 900, Output: 200}}
	if !strings.HasPrefix(cc.exceeded(c), ErrBudget) {
		t.Fatal("token cap not enforced")
	}
	cc = &CostControl{MaxAmount: 1, Currency: "USD", FX: pricing.DefaultFX}
	if cc.exceeded(pricing.Cost{Source: pricing.SourceSubscription, Amount: 5}) != "" {
		t.Fatal("subscription runs have no money cap")
	}
}
