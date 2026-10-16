package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// Provider management for the WebUI. User profiles are stored as JSON in the settings table
// ("providers.user"); hidden built-ins in "providers.hidden"; the default in
// "providers.default". API keys go to the encrypted secret store as "provider:<name>" and are
// never returned, only "set" + last 4 characters.

const (
	setProvidersUser    = "providers.user"
	setProvidersHidden  = "providers.hidden"
	setProvidersDefault = "providers.default"
	setProvidersOrder   = "providers.order"
)

var providerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

type providerAdmin struct{ a *App }

func (a *App) loadJSONSetting(ctx context.Context, key string, v any) {
	if raw, err := a.Auth.GetSetting(ctx, key); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), v)
	}
}

func (a *App) saveJSONSetting(ctx context.Context, key string, v any) error {
	b, _ := json.Marshal(v)
	return a.Auth.SetSetting(ctx, key, string(b))
}

// applyProviderSettings merges stored user providers into the registry (called at Open).
func (a *App) applyProviderSettings(ctx context.Context) {
	var hidden []string
	a.loadJSONSetting(ctx, setProvidersHidden, &hidden)
	for _, h := range hidden {
		a.Registry.Delete(h)
	}
	var user []provider.Profile
	a.loadJSONSetting(ctx, setProvidersUser, &user)
	for _, p := range user {
		if _, err := a.Registry.Put(p); err != nil {
			a.Log.Warn("stored provider", "name", p.Name, "error", err)
		}
	}
	var def string
	a.loadJSONSetting(ctx, setProvidersDefault, &def)
	if _, ok := a.Registry.Get(def); ok {
		a.Cfg.DefaultProvider = def
	}
}

func (pa providerAdmin) keyInfo(p provider.Profile) (bool, string, string) {
	if k, err := pa.a.Secrets.Get("provider:" + p.Name); err == nil && k != "" {
		return true, last4(k), "store"
	}
	if k, err := provider.EnvFromOS(p); err == nil && k != "" && provider.NormalizeSecretEnv(p.SecretEnv) != "" && os.Getenv(provider.NormalizeSecretEnv(p.SecretEnv)) != "" {
		return true, "", "env" // never echo any part of an environment key
	}
	return false, "", ""
}

func last4(k string) string {
	if len(k) < 8 {
		return ""
	}
	return k[len(k)-4:]
}

func (pa providerAdmin) usedBy(ctx context.Context) map[string]int {
	out := map[string]int{}
	tasks, _ := pa.a.Tasks.List(ctx)
	for _, t := range tasks {
		if t.Spec != nil && t.Spec.Provider != "" {
			out[t.Spec.Provider]++
		}
	}
	return out
}

func (pa providerAdmin) view(p provider.Profile, used map[string]int) webui.ProviderView {
	set, l4, src := pa.keyInfo(p)
	_, builtin := provider.LookupPreset(p.Name)
	extra := p.ExtraEnv
	if extra == nil {
		extra = map[string]string{}
	}
	return webui.ProviderView{Name: p.Name, Preset: p.Preset, BaseURL: p.BaseURL, AuthEnv: p.AuthEnv, Model: p.Model,
		SmallFastModel: p.SmallFastModel, OpusModel: p.OpusModel, SonnetModel: p.SonnetModel, HaikuModel: p.HaikuModel,
		ExtraEnv: extra, KeySet: set, KeyLast4: l4, KeySource: src, Builtin: builtin && p.Preset == p.Name,
		Default: p.Name == pa.a.Cfg.DefaultProvider, UsedBy: used[p.Name]}
}

func (pa providerAdmin) ListProviders(ctx context.Context) []webui.ProviderView {
	used := pa.usedBy(ctx)
	out := []webui.ProviderView{}
	for _, p := range pa.a.Registry.List() {
		out = append(out, pa.view(p, used))
	}
	var order []string
	pa.a.loadJSONSetting(ctx, setProvidersOrder, &order)
	rank := map[string]int{}
	for i, n := range order {
		rank[n] = i + 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[out[i].Name], rank[out[j].Name]
		if ri == 0 {
			ri = 1 << 20
		}
		if rj == 0 {
			rj = 1 << 20
		}
		return ri < rj
	})
	return out
}

// SetProviderOrder stores the display order (unknown names are dropped).
func (pa providerAdmin) SetProviderOrder(ctx context.Context, names []string) error {
	keep := []string{}
	seen := map[string]bool{}
	for _, n := range names {
		if _, ok := pa.a.Registry.Get(n); ok && !seen[n] {
			keep = append(keep, n)
			seen[n] = true
		}
	}
	return pa.a.saveJSONSetting(ctx, setProvidersOrder, keep)
}

func (pa providerAdmin) Presets() []map[string]any {
	out := []map[string]any{}
	for _, p := range provider.BuiltIn() {
		out = append(out, map[string]any{"id": p.ID, "name": p.DisplayName, "base_url": p.BaseURL, "auth_env": p.AuthEnv,
			"model": p.DefaultModel, "small_fast_model": p.SmallFastModel, "opus_model": p.OpusModel, "sonnet_model": p.SonnetModel,
			"haiku_model": p.HaikuModel, "extra_env": p.ExtraEnv, "notes": p.Notes, "source_url": p.SourceURL})
	}
	out = append(out, map[string]any{"id": "custom", "name": "Custom (Anthropic-compatible)", "base_url": "", "auth_env": "ANTHROPIC_AUTH_TOKEN"})
	return out
}

func (pa providerAdmin) SaveProvider(ctx context.Context, in webui.ProviderInput) (webui.ProviderView, error) {
	a := pa.a
	in.Name = strings.TrimSpace(in.Name)
	if !providerNameRe.MatchString(in.Name) {
		return webui.ProviderView{}, errors.New("name: lowercase letters, digits, . _ - (max 40)")
	}
	if err := webui.ValidateBaseURL(strings.TrimSpace(in.BaseURL)); err != nil {
		return webui.ProviderView{}, err
	}
	if err := webui.ValidateExtraEnv(in.ExtraEnv); err != nil {
		return webui.ProviderView{}, err
	}
	if in.AuthEnv != "ANTHROPIC_AUTH_TOKEN" && in.AuthEnv != "ANTHROPIC_API_KEY" {
		in.AuthEnv = "ANTHROPIC_AUTH_TOKEN"
	}
	if in.Preset == "custom" {
		in.Preset = ""
	}
	if in.Preset != "" {
		if _, ok := provider.LookupPreset(in.Preset); !ok {
			return webui.ProviderView{}, errors.New("unknown preset")
		}
	}
	renaming := in.OriginalName != "" && in.OriginalName != in.Name
	if _, exists := a.Registry.Get(in.Name); exists && (in.OriginalName == "" || renaming) {
		return webui.ProviderView{}, fmt.Errorf("a provider named %s already exists", in.Name)
	}
	if in.OriginalName != "" {
		if _, ok := a.Registry.Get(in.OriginalName); !ok {
			return webui.ProviderView{}, errors.New("provider not found")
		}
	}
	p := provider.Profile{Name: in.Name, Preset: in.Preset, BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), AuthEnv: in.AuthEnv,
		Model: strings.TrimSpace(in.Model), SmallFastModel: strings.TrimSpace(in.SmallFastModel), OpusModel: strings.TrimSpace(in.OpusModel),
		SonnetModel: strings.TrimSpace(in.SonnetModel), HaikuModel: strings.TrimSpace(in.HaikuModel), ExtraEnv: in.ExtraEnv}
	if in.Preset != "" {
		pre, _ := provider.LookupPreset(in.Preset)
		p.SecretEnv = provider.FromPreset(pre).SecretEnv
	}
	// key first, so a failing secret store doesn't leave a provider without its key
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		k := strings.TrimSpace(*in.APIKey)
		if len(k) > 512 || strings.ContainsAny(k, "\r\n") {
			return webui.ProviderView{}, errors.New("API key looks malformed")
		}
		if err := a.Secrets.Set("provider:"+in.Name, k); err != nil {
			return webui.ProviderView{}, errors.New("could not store the API key")
		}
	} else if renaming {
		if k, err := a.Secrets.Get("provider:" + in.OriginalName); err == nil && k != "" {
			if err := a.Secrets.Set("provider:"+in.Name, k); err != nil {
				return webui.ProviderView{}, errors.New("could not move the API key")
			}
		}
	}
	if in.ClearKey && (in.APIKey == nil || *in.APIKey == "") {
		_ = a.Secrets.Delete("provider:" + in.Name)
	}
	var user []provider.Profile
	a.loadJSONSetting(ctx, setProvidersUser, &user)
	kept := user[:0]
	for _, u := range user {
		if u.Name != in.Name && u.Name != in.OriginalName {
			kept = append(kept, u)
		}
	}
	kept = append(kept, p)
	if err := a.saveJSONSetting(ctx, setProvidersUser, kept); err != nil {
		return webui.ProviderView{}, err
	}
	res, err := a.Registry.Put(p)
	if err != nil {
		return webui.ProviderView{}, err
	}
	if renaming {
		_ = pa.removeName(ctx, in.OriginalName, false)
		_ = a.Secrets.Delete("provider:" + in.OriginalName)
		if a.Cfg.DefaultProvider == in.OriginalName {
			_ = pa.SetDefaultProvider(ctx, in.Name)
		}
	}
	a.Log.Info("provider saved", "name", in.Name, "base_url", res.BaseURL, "key_updated", in.APIKey != nil && *in.APIKey != "")
	return pa.view(res, pa.usedBy(ctx)), nil
}

func (pa providerAdmin) removeName(ctx context.Context, name string, hide bool) error {
	a := pa.a
	var user []provider.Profile
	a.loadJSONSetting(ctx, setProvidersUser, &user)
	kept := user[:0]
	for _, u := range user {
		if u.Name != name {
			kept = append(kept, u)
		}
	}
	if err := a.saveJSONSetting(ctx, setProvidersUser, kept); err != nil {
		return err
	}
	if _, builtin := provider.LookupPreset(name); builtin && hide {
		var hidden []string
		a.loadJSONSetting(ctx, setProvidersHidden, &hidden)
		hidden = append(hidden, name)
		_ = a.saveJSONSetting(ctx, setProvidersHidden, hidden)
	}
	a.Registry.Delete(name)
	return nil
}

func (pa providerAdmin) DeleteProvider(ctx context.Context, name string) error {
	if _, ok := pa.a.Registry.Get(name); !ok {
		return errors.New("provider not found")
	}
	if name == pa.a.Cfg.DefaultProvider {
		return errors.New("this is the default provider; pick another default first")
	}
	if n := pa.usedBy(ctx)[name]; n > 0 {
		return fmt.Errorf("%d task(s) use this provider; switch them first", n)
	}
	if err := pa.removeName(ctx, name, true); err != nil {
		return err
	}
	_ = pa.a.Secrets.Delete("provider:" + name)
	return nil
}

func (pa providerAdmin) SetDefaultProvider(ctx context.Context, name string) error {
	if _, ok := pa.a.Registry.Get(name); !ok {
		return errors.New("provider not found")
	}
	if err := pa.a.saveJSONSetting(ctx, setProvidersDefault, name); err != nil {
		return err
	}
	pa.a.Cfg.DefaultProvider = name
	return nil
}

// providerTestURL lets tests point at a fake endpoint.
var providerTestClient = &http.Client{Timeout: 20 * time.Second}

// TestProvider sends a 1-token Messages request to the provider's Anthropic-compatible endpoint.
func (pa providerAdmin) TestProvider(ctx context.Context, name, onlyModel string) (map[string]any, error) {
	p, ok := pa.a.Registry.Get(name)
	if !ok {
		return nil, errors.New("provider not found")
	}
	key, err := pa.a.ResolveAPIKey(p)
	if err != nil || key == "" {
		return map[string]any{"ok": false, "error": "no API key set"}, nil
	}
	model := p.Model
	if onlyModel != "" {
		model = onlyModel
	}
	if model == "" {
		model = p.SmallFastModel
	}
	if model == "" {
		model = "claude-haiku-4-5"
	}
	base := p.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "ping"}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("bad base URL")
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if p.AuthEnv == "ANTHROPIC_API_KEY" {
		req.Header.Set("x-api-key", key)
	} else {
		req.Header.Set("authorization", "Bearer "+key)
	}
	start := time.Now()
	resp, err := providerTestClient.Do(req)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "latency_ms": ms, "error": provider.MaskSecret(err.Error(), key)}, nil
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	out := map[string]any{"ok": resp.StatusCode == 200, "status": resp.StatusCode, "latency_ms": ms, "model": model}
	if resp.StatusCode != 200 {
		msg := string(rb)
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(rb, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		out["error"] = provider.MaskSecret(msg, key)
	}
	return out, nil
}
