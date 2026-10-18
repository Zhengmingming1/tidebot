package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/pricing/catalog"
)

func TestPriceCatalogRefresh(t *testing.T) {
	defer catalog.Reset()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b := catalog.Bundled()
	next := *b
	next.Generated = b.Time().Add(48 * time.Hour).Format(time.RFC3339)
	next.Presets = map[string][]pricing.ModelPrice{}
	for k, v := range b.Presets {
		next.Presets[k] = v
	}
	ds := append([]pricing.ModelPrice{}, b.Presets["deepseek"]...)
	ds[0].Input = pricing.F(0.42) // a vendor price change shipped via the catalog
	next.Presets["deepseek"] = ds
	body, _ := json.Marshal(next)
	sig := catalog.Sign(priv, body)
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path+" ua="+r.UserAgent()+" q="+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/prices.json"):
			_, _ = w.Write(body)
		case strings.HasSuffix(r.URL.Path, "/prices.json.sig"):
			_, _ = w.Write([]byte(sig))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("TIDEBOT_PRICE_CATALOG_URL", srv.URL+"/cat")
	t.Setenv("TIDEBOT_UPDATE_PUBKEY", base64.StdEncoding.EncodeToString(pub))
	a := newTestApp(t)
	ctx := context.Background()
	if !a.PriceCatalogSettings(ctx).Auto {
		t.Fatal("auto refresh should default on (like the update check)")
	}
	st, err := a.CheckPriceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st["origin"] != "remote" {
		t.Fatalf("status %v", st)
	}
	for _, h := range hits {
		if !strings.Contains(h, "ua=tidebot-updater") || !strings.HasSuffix(h, "q=") {
			t.Fatalf("request carries more than a plain GET: %s", h)
		}
	}
	p, _ := a.Registry.Get("deepseek")
	ep := p.EffectivePricing()
	if ep.Origin != "official" || *ep.Prices[0].Input != 0.42 {
		t.Fatalf("catalog official price not used: %+v", ep.Prices[0])
	}
	// same catalog again: up to date, not an error
	st, err = a.CheckPriceCatalog(ctx)
	if err != nil || st["state"].(priceCatalogState).Result != "current" {
		t.Fatalf("second check: %v %v", st["state"], err)
	}
	// user override still wins
	if _, err := a.setProviderPricing(ctx, pricingSetInput{Provider: "deepseek", Billing: pricing.Billing{Mode: pricing.ModeMetered},
		Prices: []pricing.ModelPrice{{Model: ds[0].Model, Currency: pricing.CNY, Input: pricing.F(9), Output: pricing.F(9)}}}); err != nil {
		t.Fatal(err)
	}
	p, _ = a.Registry.Get("deepseek")
	if ep := p.EffectivePricing(); ep.Origin != "custom" || *ep.Prices[0].Input != 9 {
		t.Fatalf("override lost: %+v", ep)
	}
	// a bad signature is refused and reported
	sig = base64.StdEncoding.EncodeToString(make([]byte, 64))
	next.Generated = b.Time().Add(96 * time.Hour).Format(time.RFC3339)
	body, _ = json.Marshal(next)
	st, err = a.CheckPriceCatalog(ctx)
	if err == nil || st["state"].(priceCatalogState).Result != "error" {
		t.Fatalf("bad signature accepted: %v", st["state"])
	}
}
