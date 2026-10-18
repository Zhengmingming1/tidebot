// Command pricecat builds and signs tidebot's price catalog.
//
//	pricecat gen [-modelsdev api.json|URL] [-generated RFC3339] [-out file]
//	    writes the catalog: our verified preset prices (internal/provider/prices.go)
//	    plus maker list prices from models.dev (USD per 1M tokens).
//	pricecat sign <file>
//	    writes <file>.sig with $UPDATE_SIGNING_KEY (never printed).
//
// The bundled copy lives at internal/pricing/catalog/prices.json; refresh it
// with `go generate ./internal/pricing/catalog` before a release. The weekly
// signed copy is published by .github/workflows/price-catalog.yml.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/pricing/catalog"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

const modelsDevURL = "https://models.dev/api.json"

// makers are models.dev providers that are the model's own vendor; resellers
// and routers are skipped so the fallback is the list price. First one wins.
var makers = []string{"anthropic", "openai", "google", "deepseek", "moonshotai", "zhipuai", "zai", "alibaba", "xai", "mistral", "minimax"}

func main() {
	if len(os.Args) < 2 {
		fail("usage: pricecat gen [flags] | pricecat sign <file>")
	}
	switch os.Args[1] {
	case "gen":
		fs := flag.NewFlagSet("gen", flag.ExitOnError)
		src := fs.String("modelsdev", modelsDevURL, "models.dev api.json path or URL")
		gen := fs.String("generated", "", "generated time (RFC 3339); default now")
		out := fs.String("out", "prices.json", "output file")
		_ = fs.Parse(os.Args[2:])
		b, err := build(*src, *gen)
		check(err)
		check(os.WriteFile(*out, b, 0o644))
		c, err := catalog.Parse(b)
		check(err)
		p, m := c.Count()
		fmt.Printf("wrote %s: %d preset prices, %d models\n", *out, p, m)
	case "sign":
		if len(os.Args) < 3 {
			fail("pricecat sign <file>")
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("UPDATE_SIGNING_KEY")))
		if err != nil || len(key) != 64 {
			fail("UPDATE_SIGNING_KEY is missing or malformed")
		}
		body, err := os.ReadFile(os.Args[2])
		check(err)
		if _, err := catalog.Parse(body); err != nil {
			fail(err.Error())
		}
		check(os.WriteFile(os.Args[2]+".sig", []byte(catalog.Sign(key, body)), 0o644))
		fmt.Println("signed", os.Args[2])
	default:
		fail("unknown command")
	}
}

type mdCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
	Over200k   *struct {
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
	} `json:"context_over_200k"`
	Tiers []struct {
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
		Tier       struct {
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
}

type mdProvider struct {
	Name   string `json:"name"`
	Doc    string `json:"doc"`
	Models map[string]struct {
		Cost *mdCost `json:"cost"`
	} `json:"models"`
}

func load(src string) ([]byte, error) {
	if !strings.HasPrefix(src, "http") {
		return os.ReadFile(src)
	}
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Get(src)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", src, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func build(src, gen string) ([]byte, error) {
	now := time.Now().UTC()
	if gen != "" {
		t, err := time.Parse(time.RFC3339, gen)
		if err != nil {
			return nil, err
		}
		now = t.UTC()
	}
	day := now.Format("2006-01-02")
	raw, err := load(src)
	if err != nil {
		return nil, err
	}
	var md map[string]mdProvider
	if err := json.Unmarshal(raw, &md); err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}
	c := catalog.Catalog{Schema: catalog.Schema, Generated: now.Format(time.RFC3339),
		Presets: map[string][]pricing.ModelPrice{}, Models: map[string]pricing.ModelPrice{}}
	// 1. our hand-verified official prices (CN vendors in CNY, with source URLs).
	urls := map[string]string{}
	for _, p := range provider.BuiltIn() {
		if len(p.Prices) == 0 {
			continue
		}
		c.Presets[p.ID] = p.Prices
		for _, mp := range p.Prices {
			if mp.Source != "" && urls[mp.Source] == "" {
				urls[mp.Source] = mp.Fetched
			}
		}
	}
	srcs := make([]string, 0, len(urls))
	for u := range urls {
		srcs = append(srcs, u)
	}
	sort.Strings(srcs)
	for _, u := range srcs {
		c.Sources = append(c.Sources, catalog.Source{ID: "official", Name: hostOf(u), URL: u, Fetched: urls[u]})
	}
	// 2. maker list prices from models.dev.
	srcURL := src
	if !strings.HasPrefix(srcURL, "http") {
		srcURL = modelsDevURL
	}
	c.Sources = append(c.Sources, catalog.Source{ID: "models.dev", Name: "models.dev", URL: srcURL, Fetched: day})
	for _, mk := range makers {
		pv, ok := md[mk]
		if !ok {
			continue
		}
		ids := make([]string, 0, len(pv.Models))
		for id := range pv.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			k := pricing.Normalize(id)
			if _, dup := c.Models[k]; dup || strings.ContainsAny(k, " *") {
				continue
			}
			if mp, ok := convert(id, pv.Models[id].Cost, day); ok {
				c.Models[k] = mp
			}
		}
	}
	if err := sanity(&c); err != nil {
		return nil, err
	}
	return json.MarshalIndent(c, "", " ")
}

func convert(id string, cost *mdCost, day string) (pricing.ModelPrice, bool) {
	if cost == nil || cost.Input == nil || cost.Output == nil || (*cost.Input == 0 && *cost.Output == 0) {
		return pricing.ModelPrice{}, false
	}
	p := pricing.ModelPrice{Model: id, Currency: pricing.USD, Input: cost.Input, Output: cost.Output,
		CacheRead: cost.CacheRead, CacheWrite: cost.CacheWrite, Source: "models.dev", Fetched: day}
	if p.CacheWrite == nil {
		p.CacheWrite = p.Input // no separate charge listed: billed as input
	}
	type band struct {
		from          int64
		in, out, r, w *float64
	}
	var bands []band
	if o := cost.Over200k; o != nil && o.Input != nil && o.Output != nil {
		bands = append(bands, band{200_000, o.Input, o.Output, o.CacheRead, o.CacheWrite})
	}
	for _, t := range cost.Tiers {
		if t.Tier.Type == "context" && t.Tier.Size > 0 && t.Input != nil && t.Output != nil {
			bands = append(bands, band{t.Tier.Size, t.Input, t.Output, t.CacheRead, t.CacheWrite})
		}
	}
	sort.Slice(bands, func(i, j int) bool { return bands[i].from < bands[j].from })
	if len(bands) > 0 {
		prev := band{0, p.Input, p.Output, p.CacheRead, p.CacheWrite}
		for _, b := range bands {
			if b.from <= prev.from {
				continue
			}
			p.Tiers = append(p.Tiers, pricing.Tier{UpTo: b.from + 1, Input: prev.in, Output: prev.out, CacheRead: prev.r, CacheWrite: orIn(prev.w, prev.in)})
			prev = b
		}
		p.Tiers = append(p.Tiers, pricing.Tier{UpTo: 0, Input: prev.in, Output: prev.out, CacheRead: prev.r, CacheWrite: orIn(prev.w, prev.in)})
	}
	if pricing.Validate(pricing.Billing{}, []pricing.ModelPrice{p}) != nil {
		return pricing.ModelPrice{}, false
	}
	return p, true
}

func orIn(w, in *float64) *float64 {
	if w != nil {
		return w
	}
	return in
}

// sanity refuses an obviously broken upstream export.
func sanity(c *catalog.Catalog) error {
	if len(c.Models) < 50 {
		return fmt.Errorf("only %d models from models.dev; refusing to write", len(c.Models))
	}
	if len(c.Presets) == 0 {
		return fmt.Errorf("no preset prices")
	}
	return nil
}

func hostOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = s[:i]
	}
	return s
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(s string) {
	fmt.Fprintln(os.Stderr, "pricecat:", s)
	os.Exit(1)
}
