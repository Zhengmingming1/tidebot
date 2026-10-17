package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
	"github.com/Zhengmingming1/tidebot/internal/updater"
)

// NetworkSettings are stored as JSON under setting "network" (Settings → 网络).
type NetworkSettings struct {
	Proxy   netproxy.Settings `json:"proxy"`
	Mirrors []updater.Source  `json:"mirrors"`
}

func (a *App) NetworkSettings(ctx context.Context) NetworkSettings {
	s := NetworkSettings{Proxy: netproxy.Settings{Mode: netproxy.ModeAuto}, Mirrors: []updater.Source{}}
	if raw, _ := a.Auth.GetSetting(ctx, "network"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	if s.Proxy.Mode == "" {
		s.Proxy.Mode = netproxy.ModeAuto
	}
	if s.Mirrors == nil {
		s.Mirrors = []updater.Source{}
	}
	return s
}

// LoadNetwork applies the stored proxy setting process-wide. Called at start.
func (a *App) LoadNetwork(ctx context.Context) {
	s := a.NetworkSettings(ctx)
	if err := netproxy.Set(s.Proxy); err != nil {
		a.Log.Warn("stored proxy setting is invalid; using auto", "error", err)
		_ = netproxy.Set(netproxy.Settings{Mode: netproxy.ModeAuto})
	}
}

func validateNetwork(s NetworkSettings) error {
	if err := netproxy.Validate(s.Proxy); err != nil {
		return err
	}
	if len(s.Mirrors) > 8 {
		return errors.New("at most 8 mirrors")
	}
	for _, m := range s.Mirrors {
		if err := m.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) networkView(ctx context.Context) map[string]any {
	return map[string]any{
		"settings":      a.NetworkSettings(ctx),
		"effective":     netproxy.Resolve(),
		"presets":       updater.Presets,
		"releases_page": updater.ReleasesPage,
	}
}

func (a *App) networkRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"network.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.networkView(ctx), nil },
		"network.set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			s, err := decode[NetworkSettings](raw)
			if err != nil {
				return nil, err
			}
			if s.Proxy.Mode == "" {
				s.Proxy.Mode = netproxy.ModeAuto
			}
			if err := validateNetwork(s); err != nil {
				return nil, err
			}
			b, _ := json.Marshal(s)
			if err := a.Auth.SetSetting(ctx, "network", string(b)); err != nil {
				return nil, err
			}
			_ = netproxy.Set(s.Proxy)
			return a.networkView(ctx), nil
		},
		// network.test checks the update source with the given (unsaved) settings,
		// or the saved ones: every source in order, with a classified error each.
		"network.test": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				Settings *NetworkSettings `json:"settings"`
			}](raw)
			s := a.NetworkSettings(ctx)
			if p.Settings != nil {
				if err := validateNetwork(*p.Settings); err != nil {
					return nil, err
				}
				s = *p.Settings
			}
			saved := netproxy.Get()
			_ = netproxy.Set(s.Proxy)
			defer func() { _ = netproxy.Set(saved) }()
			eff := netproxy.Resolve()
			hc := &http.Client{Transport: netproxy.Transport()}
			out := []map[string]any{}
			for _, src := range updater.Sources(s.Mirrors) {
				tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				t0 := time.Now()
				m, err := updater.Fetch(tctx, hc, src.ManifestURL(), a.su().pubKey)
				cancel()
				r := map[string]any{"source": src.Name, "ms": time.Since(t0).Milliseconds()}
				if px := netproxy.ProxyFor(src.ManifestURL()); px != nil {
					r["proxy"] = netproxy.Redact(px.String())
				}
				if err != nil {
					r["ok"], r["code"], r["error"] = false, updater.Classify(err), err.Error()
				} else {
					r["ok"], r["version"] = true, m.Version
				}
				out = append(out, r)
			}
			return map[string]any{"effective": eff, "results": out, "releases_page": updater.ReleasesPage}, nil
		},
	}
}
