package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing/catalog"
	"github.com/Zhengmingming1/tidebot/internal/updater"
)

// Price catalog refresh: a signed prices.json published next to the releases
// (tag "price-catalog" on the public releases repo), fetched weekly through
// the same proxy and mirrors as the updater. Plain GETs only: no version,
// machine id or usage is sent. Off with Settings → 价格目录.

const (
	setPriceCatalog      = "pricing.catalog"
	setPriceCatalogState = "pricing.catalog.state"
	priceCatalogEvery    = 7 * 24 * time.Hour
	priceCatalogTag      = "price-catalog"
)

// PriceCatalogSettings is the user's choice.
type PriceCatalogSettings struct {
	Auto bool `json:"auto"`
}

// priceCatalogState is the last check.
type priceCatalogState struct {
	Checked string `json:"checked,omitempty"` // RFC 3339
	OK      bool   `json:"ok"`
	Result  string `json:"result,omitempty"` // installed | current | error
	Error   string `json:"error,omitempty"`
	From    string `json:"from,omitempty"` // source name that answered
}

func (a *App) PriceCatalogSettings(ctx context.Context) PriceCatalogSettings {
	s := PriceCatalogSettings{Auto: true} // same default as the update check
	if a.Auth != nil {
		a.loadJSONSetting(ctx, setPriceCatalog, &s)
	}
	return s
}

func (a *App) priceCatalogDir() string { return filepath.Join(a.Cfg.DataDir, "price-catalog") }

// LoadPriceCatalog activates a previously downloaded, still-valid catalog.
func (a *App) LoadPriceCatalog() {
	if c, err := catalog.Load(a.priceCatalogDir(), a.su().pubKey); err != nil {
		a.Log.Warn("saved price catalog ignored", "error", err)
	} else if c != nil {
		a.Log.Info("price catalog loaded", "generated", c.Generated)
	}
}

// priceCatalogBase is where prices.json(.sig) live on GitHub.
func priceCatalogBase() string {
	if v := strings.TrimRight(os.Getenv("TIDEBOT_PRICE_CATALOG_URL"), "/"); v != "" {
		return v
	}
	return "https://github.com/" + updater.ReleasesRepo + "/releases/download/" + priceCatalogTag
}

// priceCatalogURL maps an update source to the catalog directory: a prefix
// mirror wraps the GitHub URL; a self-hosted manifest mirror serves
// prices.json next to latest.json.
func priceCatalogURL(s updater.Source) string {
	switch {
	case s.Manifest != "":
		return strings.TrimRight(s.Manifest, "/")
	case s.Prefix != "":
		return s.Prefix + priceCatalogBase()
	}
	return priceCatalogBase()
}

// CheckPriceCatalog downloads, verifies and installs a newer catalog.
func (a *App) CheckPriceCatalog(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	srcs := updater.Sources(a.NetworkSettings(ctx).Mirrors)
	if os.Getenv("TIDEBOT_PRICE_CATALOG_URL") != "" {
		srcs = []updater.Source{updater.Direct}
	}
	st := priceCatalogState{Checked: time.Now().UTC().Format(time.RFC3339)}
	var lastErr error
	for _, s := range srcs {
		base := priceCatalogURL(s)
		hc := a.su().client
		body, err := updater.Get(ctx, hc, base+"/"+catalog.FileName, catalog.MaxSize)
		if err == nil {
			var sig []byte
			if sig, err = updater.Get(ctx, hc, base+"/"+catalog.SigName, 4096); err == nil {
				_, err = catalog.Install(a.priceCatalogDir(), a.su().pubKey, body, string(sig))
			}
		}
		switch {
		case err == nil:
			st.OK, st.Result, st.From = true, "installed", s.Name
		case errors.Is(err, catalog.ErrNotNewer):
			st.OK, st.Result, st.From = true, "current", s.Name
		case isNotFound(err):
			st.OK, st.Result, st.From = true, "none", s.Name // nothing published yet: the bundled catalog stays
		default:
			lastErr = err
			continue
		}
		break
	}
	if !st.OK {
		st.Result = "error"
		if lastErr != nil {
			st.Error = lastErr.Error()
		}
	}
	_ = a.saveJSONSetting(ctx, setPriceCatalogState, st)
	if !st.OK {
		return a.PriceCatalogStatus(ctx), lastErr
	}
	return a.PriceCatalogStatus(ctx), nil
}

// PriceCatalogStatus is what Settings → 价格目录 shows.
func (a *App) PriceCatalogStatus(ctx context.Context) map[string]any {
	var st priceCatalogState
	a.loadJSONSetting(ctx, setPriceCatalogState, &st)
	b, c := catalog.Bundled(), catalog.Active()
	view := func(x *catalog.Catalog) map[string]any {
		p, m := x.Count()
		return map[string]any{"generated": x.Generated, "presets": p, "models": m, "sources": x.Sources}
	}
	origin := "bundled"
	if c != b {
		origin = "remote"
	}
	return map[string]any{
		"settings": a.PriceCatalogSettings(ctx), "state": st, "origin": origin,
		"bundled": view(b), "active": view(c), "url": priceCatalogBase(), "every_days": int(priceCatalogEvery.Hours() / 24),
	}
}

// runPriceCatalog checks weekly while Auto is on.
func (a *App) runPriceCatalog(ctx context.Context) {
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if a.PriceCatalogSettings(ctx).Auto {
			var st priceCatalogState
			a.loadJSONSetting(ctx, setPriceCatalogState, &st)
			t, _ := time.Parse(time.RFC3339, st.Checked)
			if time.Since(t) >= priceCatalogEvery {
				if _, err := a.CheckPriceCatalog(ctx); err != nil {
					a.Log.Info("price catalog check failed", "error", err)
				}
			}
		}
		timer.Reset(6 * time.Hour)
	}
}

func (a *App) priceCatalogRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"pricing.catalog.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.PriceCatalogStatus(ctx), nil },
		"pricing.catalog.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[PriceCatalogSettings](raw)
			if err != nil {
				return nil, err
			}
			if err := a.saveJSONSetting(ctx, setPriceCatalog, in); err != nil {
				return nil, err
			}
			return a.PriceCatalogStatus(ctx), nil
		},
		"pricing.catalog.check": func(ctx context.Context, _ json.RawMessage) (any, error) {
			st, _ := a.CheckPriceCatalog(ctx) // the outcome is in state.result / state.error
			return st, nil
		},
	}
}

func isNotFound(err error) bool {
	var he *updater.HTTPError
	return errors.As(err, &he) && he.Status == 404
}
