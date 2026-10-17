package provider

import (
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

func TestPresetPricesHaveSources(t *testing.T) {
	for _, p := range BuiltIn() {
		if err := pricing.Validate(pricing.Billing{Mode: pricing.ModeMetered}, p.Prices); err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		for _, m := range p.Prices {
			if !strings.HasPrefix(m.Source, "https://") || m.Fetched == "" || m.Input == nil || m.Output == nil {
				t.Fatalf("%s/%s: every shipped price needs an official source + date", p.ID, m.Model)
			}
		}
	}
}

func TestEffectivePricing(t *testing.T) {
	p := FromPreset(mustPreset(t, "kimi-cn"))
	ep := p.EffectivePricing()
	if ep.Origin != "official" || ep.Prices[0].Currency != pricing.CNY || ep.Billing.Mode != pricing.ModeMetered {
		t.Fatalf("%+v", ep)
	}
	p.Prices = []pricing.ModelPrice{{Model: "kimi-k3", Currency: pricing.CNY, Input: pricing.F(1), Output: pricing.F(2)}}
	if ep := p.EffectivePricing(); ep.Origin != "custom" || len(ep.Official) == 0 {
		t.Fatalf("%+v", ep)
	}
	sub := FromPreset(mustPreset(t, "kimi-coding")).EffectivePricing()
	if sub.Billing.Mode != pricing.ModeSubscription || sub.Origin != "none" {
		t.Fatalf("%+v", sub)
	}
	if !FromPreset(mustPreset(t, "anthropic")).EffectivePricing().Anthropic {
		t.Fatal("anthropic official")
	}
	if FromPreset(mustPreset(t, "deepseek")).EffectivePricing().Anthropic {
		t.Fatal("deepseek is not anthropic")
	}
}

func mustPreset(t *testing.T, id string) Preset {
	t.Helper()
	p, ok := LookupPreset(id)
	if !ok {
		t.Fatal(id)
	}
	return p
}
