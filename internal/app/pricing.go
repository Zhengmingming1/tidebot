package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/pricing/catalog"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// Pricing: tidebot prices runs itself from stream-json token usage using the
// provider's per-model prices (internal/pricing). Claude Code's
// total_cost_usd is only a fallback for Anthropic's own API.

const setPricingFX = "pricing.fx"

// FX returns the stored display currency / fixed rate.
func (a *App) FX(ctx context.Context) pricing.FX {
	f := pricing.DefaultFX
	if a.Auth != nil {
		a.loadJSONSetting(ctx, setPricingFX, &f)
	}
	if f.Validate() != nil {
		return pricing.DefaultFX
	}
	return f
}

// SetFX stores the display currency / rate and converts budgets to it.
func (a *App) SetFX(ctx context.Context, f pricing.FX) error {
	if err := f.Validate(); err != nil {
		return err
	}
	if err := a.saveJSONSetting(ctx, setPricingFX, f); err != nil {
		return err
	}
	a.fxCache.Store(&f)
	if a.Cost != nil {
		return a.Cost.ConvertBudgets(ctx, f, f.Display)
	}
	return nil
}

func (a *App) cachedFX() pricing.FX {
	if f := a.fxCache.Load(); f != nil {
		return *f
	}
	f := a.FX(context.Background())
	a.fxCache.Store(&f)
	return f
}

// officialAnthropic reports whether Claude Code's own cost figure is valid
// for this profile (Anthropic's API, or no provider override at all).
func officialAnthropic(p provider.Profile) bool {
	if p.EffectivePricing().Anthropic {
		return true
	}
	return p.Preset == "" && p.BaseURL == ""
}

// calculator builds the pricing calculator for a profile.
func calculator(p provider.Profile) pricing.Calculator {
	ep := p.EffectivePricing()
	return pricing.Calculator{Billing: ep.Billing, Prices: ep.Prices, Fallback: p.Model, Official: officialAnthropic(p), Lookup: catalog.Active().Lookup}
}

// costControl builds the run's pricing + mid-run caps. runMax is in the
// display currency. For subscription providers it checks the quota window
// first and caps the run at the remaining tokens/requests.
func (a *App) costControl(ctx context.Context, p provider.Profile, runMax float64) (*runner.CostControl, error) {
	f := a.cachedFX()
	cc := &runner.CostControl{Calc: calculator(p), FX: f, Currency: f.Display, MaxAmount: runMax}
	b := cc.Calc.Billing
	if b.Mode == pricing.ModeSubscription && b.Quota != nil && a.Cost != nil {
		q := b.Quota
		tok, req, _, err := a.Cost.QuotaUsage(ctx, p.Name, q.Window)
		if err != nil {
			return nil, err
		}
		if q.MaxTokens > 0 {
			if tok >= q.MaxTokens {
				return nil, fmt.Errorf("subscription quota reached for %s: %d/%d tokens in the %s window", p.Name, tok, q.MaxTokens, q.Window)
			}
			cc.MaxTokens = q.MaxTokens - tok
		}
		if q.MaxRequests > 0 {
			if req >= q.MaxRequests {
				return nil, fmt.Errorf("subscription quota reached for %s: %d/%d requests in the %s window", p.Name, req, q.MaxRequests, q.Window)
			}
			cc.MaxRequests = q.MaxRequests - req
		}
	}
	if b.Mode != pricing.ModeMetered {
		cc.MaxAmount = 0
	}
	return cc, nil
}

// budgetArgs keeps --max-budget-usd only where Claude Code's figure is
// meaningful (Anthropic's API, metered); elsewhere tidebot enforces the cap
// from its own token accounting. For Anthropic the value is converted to USD.
func (a *App) budgetArgs(args []string, p provider.Profile, runMax float64) []string {
	keep := officialAnthropic(p) && calculator(p).Billing.Mode == pricing.ModeMetered
	f := a.cachedFX()
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--max-budget-usd" {
			if i+1 < len(args) {
				i++
			}
			if keep && runMax > 0 {
				out = append(out, "--max-budget-usd", fmt.Sprintf("%.4f", f.Convert(runMax, f.Display, pricing.USD)))
			}
			continue
		}
		if strings.HasPrefix(args[i], "--max-budget-usd=") {
			if keep && runMax > 0 {
				out = append(out, fmt.Sprintf("--max-budget-usd=%.4f", f.Convert(runMax, f.Display, pricing.USD)))
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// callerCapUSD returns a --max-budget-usd value a caller put in ExtraArgs (0 = none).
func callerCapUSD(args []string) float64 {
	for i, a := range args {
		v := ""
		if a == "--max-budget-usd" && i+1 < len(args) {
			v = args[i+1]
		} else if strings.HasPrefix(a, "--max-budget-usd=") {
			v = strings.TrimPrefix(a, "--max-budget-usd=")
		}
		if v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				return f
			}
		}
	}
	return 0
}

// RunCostUSD is a run's tidebot-computed cost in USD (fixed rate).
func (a *App) RunCostUSD(res runner.Result) float64 {
	return a.cachedFX().Convert(res.Cost.Amount, res.Cost.Currency, pricing.USD)
}

// pricingView is what the provider drawer edits.
func (a *App) pricingView(name string) (map[string]any, error) {
	p, ok := a.Registry.Get(name)
	if !ok {
		return nil, errors.New("provider not found")
	}
	ep := p.EffectivePricing()
	ep.Anthropic = officialAnthropic(p)
	// Fallback: what an unpriced default model would be estimated at (catalog list price).
	var fb *pricing.ModelPrice
	if ep.Billing.Mode == pricing.ModeMetered && !ep.Anthropic && pricing.Find(ep.Prices, p.Model) == nil {
		fb = catalog.Active().Lookup(p.Model)
	}
	return map[string]any{"provider": name, "pricing": ep, "model": p.Model, "fallback": fb, "catalog": catalog.Active().Generated}, nil
}

type pricingSetInput struct {
	Provider string               `json:"provider"`
	Billing  pricing.Billing      `json:"billing"`
	Prices   []pricing.ModelPrice `json:"prices"`
	// Restore drops the custom price list and goes back to the official one.
	Restore bool `json:"restore"`
}

// setProviderPricing stores billing/prices on the provider's user profile.
func (a *App) setProviderPricing(ctx context.Context, in pricingSetInput) (map[string]any, error) {
	p, ok := a.Registry.Get(in.Provider)
	if !ok {
		return nil, errors.New("provider not found")
	}
	if in.Billing.Mode == "" {
		in.Billing.Mode = pricing.ModeMetered
	}
	if in.Restore {
		p.Prices = nil
	} else {
		if in.Prices == nil {
			in.Prices = []pricing.ModelPrice{}
		}
		for i := range in.Prices {
			in.Prices[i].Source, in.Prices[i].Fetched = "", "" // user-entered
		}
		p.Prices = in.Prices
	}
	if err := pricing.Validate(in.Billing, p.Prices); err != nil {
		return nil, err
	}
	b := in.Billing
	p.Billing = &b
	var user []provider.Profile
	a.loadJSONSetting(ctx, setProvidersUser, &user)
	found := false
	for i := range user {
		if user[i].Name == p.Name {
			user[i].Billing, user[i].Prices = p.Billing, p.Prices
			found = true
		}
	}
	if !found {
		user = append(user, p)
	}
	if err := a.saveJSONSetting(ctx, setProvidersUser, user); err != nil {
		return nil, err
	}
	if _, err := a.Registry.Put(p); err != nil {
		return nil, err
	}
	return a.pricingView(p.Name)
}

func (a *App) pricingRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"pricing.get": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				Provider string `json:"provider"`
			}](raw)
			if err != nil {
				return nil, err
			}
			return a.pricingView(in.Provider)
		},
		"pricing.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[pricingSetInput](raw)
			if err != nil {
				return nil, err
			}
			return a.setProviderPricing(ctx, in)
		},
		"pricing.fx.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.FX(ctx), nil },
		"pricing.fx.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			f, err := decode[pricing.FX](raw)
			if err != nil {
				return nil, err
			}
			if err := a.SetFX(ctx, f); err != nil {
				return nil, err
			}
			return a.FX(ctx), nil
		},
	}
}
