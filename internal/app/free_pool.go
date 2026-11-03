package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/freepool"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

const freePoolNetworkPermission = "network:ai-gateway.kilo.ai"
const freePoolUpstream = "https://ai-gateway.kilo.ai/api/v1"
const freePoolDefaultModel = "kilo-auto/free"

var errManagedProvider = errors.New("this provider is managed by the Kilo Free Pool plugin; change its plugin settings instead")

type freePoolState struct {
	transition sync.Mutex
	mu         sync.Mutex
	gateway    *freepool.Gateway
	err        error
	closed     bool
}

func managedFreePool(p provider.Profile) bool { return p.ManagedBy == freepool.PluginID }

func (a *App) managedProvider(name string) bool {
	p, ok := a.Registry.Get(name)
	return ok && managedFreePool(p)
}

// checkFreePool is also called by every gateway request, so revocation does not rely
// on a caller remembering to refresh the in-memory provider registry.
func (a *App) checkFreePool(ctx context.Context) (string, error) {
	a.freePool.mu.Lock()
	closed := a.freePool.closed
	a.freePool.mu.Unlock()
	if closed {
		return "", errors.New("free pool is shutting down")
	}
	in, err := a.Plugins.Get(ctx, freepool.PluginID)
	if err != nil || in.Current == nil {
		return "", errors.New("install and approve the official Kilo Free Pool plugin first")
	}
	if in.Source != plugin.SourceOfficial || plugin.OfficialVerifier == nil || !plugin.OfficialVerifier(in.ID, in.Current.ContentHash) {
		return "", errors.New("free pool requires the verified official plugin version")
	}
	if !in.Enabled {
		return "", errors.New("Kilo Free Pool plugin is disabled")
	}
	approved := false
	for _, p := range in.Current.Approved {
		approved = approved || p == freePoolNetworkPermission
	}
	if !approved {
		return "", errors.New("approve access to ai-gateway.kilo.ai in the plugin permissions first")
	}
	cfg, err := a.Plugins.GetConfig(ctx, freepool.PluginID)
	if err != nil {
		return "", err
	}
	if ack, _ := cfg.Values["acknowledge_prompt_training"].(bool); !ack {
		return "", errors.New("acknowledge that free providers may use prompts and responses for training before enabling the free pool")
	}
	model, _ := cfg.Values["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = freePoolDefaultModel
	}
	return model, nil
}

// syncFreePool rebuilds only process-local state; no port, token or managed profile
// is persisted into ordinary provider settings.
func (a *App) syncFreePool(ctx context.Context) {
	a.freePool.transition.Lock()
	defer a.freePool.transition.Unlock()
	a.freePool.mu.Lock()
	old := a.freePool.gateway
	closed := a.freePool.closed
	a.freePool.mu.Unlock()
	model, err := a.checkFreePool(ctx)
	current, exists := a.Registry.Get(freepool.ProviderName)
	if err == nil && exists && !managedFreePool(current) {
		err = fmt.Errorf("a custom provider named %s already exists; rename it before enabling the free pool", freepool.ProviderName)
	}
	// Saving an unchanged config or grant must not interrupt an in-flight run.
	if !closed && err == nil && old != nil && exists && managedFreePool(current) && current.Model == model && current.BaseURL == old.URL() {
		a.freePool.mu.Lock()
		a.freePool.err = nil
		a.freePool.mu.Unlock()
		return
	}
	a.freePool.mu.Lock()
	a.freePool.gateway = nil
	a.freePool.mu.Unlock()
	if exists && managedFreePool(current) {
		a.Registry.Delete(freepool.ProviderName)
	}
	if old != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = old.Close(closeCtx)
		cancel()
	}
	forgetModelList(freepool.ProviderName)
	if closed {
		return
	}
	var g *freepool.Gateway
	if err == nil {
		g = freepool.New(a.checkFreePool)
		err = g.Start()
	}
	if err == nil {
		_, err = a.Registry.Put(provider.Profile{
			Name: freepool.ProviderName, ManagedBy: freepool.PluginID, BaseURL: g.URL(), AuthEnv: "ANTHROPIC_API_KEY",
			Model: model, OpusModel: model, SonnetModel: model, HaikuModel: model, FableModel: model,
			SmallFastModel: model, SubagentModel: model,
			// Empty overrides remove inherited cloud routes before launching Claude.
			ExtraEnv: map[string]string{
				"CLAUDE_CODE_USE_ANTHROPIC_AWS": "", "CLAUDE_CODE_USE_BEDROCK": "", "CLAUDE_CODE_USE_FOUNDRY": "",
				"CLAUDE_CODE_USE_MANTLE": "", "CLAUDE_CODE_USE_VERTEX": "", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST": "1",
			},
			Billing: &pricing.Billing{Mode: pricing.ModeFree},
		})
	}
	if err != nil && g != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = g.Close(closeCtx)
		cancel()
		g = nil
	}
	a.freePool.mu.Lock()
	a.freePool.gateway, a.freePool.err = g, err
	a.freePool.mu.Unlock()
}

func (a *App) closeFreePool() {
	a.freePool.mu.Lock()
	a.freePool.closed = true
	a.freePool.mu.Unlock()
	a.syncFreePool(context.Background())
}

func (a *App) freePoolKey(p provider.Profile) (string, error) {
	model, err := a.checkFreePool(context.Background())
	if err != nil {
		return "", err
	}
	a.freePool.mu.Lock()
	defer a.freePool.mu.Unlock()
	g := a.freePool.gateway
	if g == nil || p.BaseURL != g.URL() || p.Model != model {
		return "", errors.New("free pool settings changed; start the run again")
	}
	return g.Token(), nil
}

func (a *App) freePoolStatus(ctx context.Context) map[string]any {
	out := map[string]any{
		"plugin_id": freepool.PluginID, "provider_name": freepool.ProviderName,
		"installed": false, "enabled": false, "approved": false, "training_acknowledged": false,
		"model": freePoolDefaultModel, "ready": false, "running": false, "upstream_url": freePoolUpstream,
		"training_notice": "Free providers may use prompts and responses for training. Requests are sent to Kilo and its upstream providers. Do not send confidential or personal data.",
	}
	if in, err := a.Plugins.Get(ctx, freepool.PluginID); err == nil {
		out["installed"], out["enabled"] = in.Current != nil, in.Enabled
		if in.Current != nil && in.Source == plugin.SourceOfficial && plugin.OfficialVerifier != nil && plugin.OfficialVerifier(in.ID, in.Current.ContentHash) {
			for _, p := range in.Current.Approved {
				if p == freePoolNetworkPermission {
					out["approved"] = true
				}
			}
		}
		if cfg, err := a.Plugins.GetConfig(ctx, freepool.PluginID); err == nil {
			if model, _ := cfg.Values["model"].(string); strings.TrimSpace(model) != "" {
				out["model"] = strings.TrimSpace(model)
			}
			out["training_acknowledged"], _ = cfg.Values["acknowledge_prompt_training"].(bool)
		}
	}
	_, err := a.checkFreePool(ctx)
	a.freePool.mu.Lock()
	out["running"] = a.freePool.gateway != nil
	if err == nil {
		err = a.freePool.err
	}
	out["ready"] = err == nil && a.freePool.gateway != nil
	a.freePool.mu.Unlock()
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}

func (a *App) freePoolModels(ctx context.Context, refresh bool) (map[string]any, error) {
	if _, err := a.checkFreePool(ctx); err != nil {
		return nil, err
	}
	a.freePool.mu.Lock()
	g := a.freePool.gateway
	a.freePool.mu.Unlock()
	if g == nil {
		return nil, errors.New("free pool is unavailable; check its plugin status")
	}
	models, err := g.Models(ctx, refresh)
	if err != nil {
		return nil, err
	}
	return map[string]any{"models": models, "upstream_url": freePoolUpstream}, nil
}
