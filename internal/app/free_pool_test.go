package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/freepool"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func configureFreePool(t *testing.T, a *App) {
	t.Helper()
	installOfficial(t, a, freepool.PluginID)
	if err := a.Plugins.SetConfig(context.Background(), freepool.PluginID, map[string]any{"acknowledge_prompt_training": true}); err != nil {
		t.Fatal(err)
	}
	if status := a.freePoolStatus(context.Background()); status["ready"] != true {
		t.Fatalf("free pool not ready: %v", status)
	}
}

func assertGatewayClosed(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(base + "/v1/models")
	if err == nil {
		resp.Body.Close()
		t.Fatal("old gateway listener remains accessible")
	}
}

func TestFreePoolApprovalLifecycleAndSecretIsolation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	defaultBefore := a.Cfg.DefaultProvider
	v, err := official.Stage(ctx, a.Plugins, freepool.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.freePoolModels(ctx, true); err == nil {
		t.Fatal("unapproved plugin may not fetch a model catalog")
	}
	if err := a.Plugins.Grant(ctx, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"acknowledge_prompt_training": true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Registry.Get(freepool.ProviderName); ok {
		t.Fatal("missing network permission registered a provider")
	}
	if err := a.Plugins.SetPermission(ctx, freepool.PluginID, freePoolNetworkPermission, true); err != nil {
		t.Fatal(err)
	}
	p, err := a.ResolveProfile(freepool.ProviderName)
	if err != nil {
		t.Fatal(err)
	}
	if p.ManagedBy != freepool.PluginID || p.Model != freepool.DefaultModel || p.EffectivePricing().Billing.Mode != pricing.ModeFree {
		t.Fatalf("bad managed profile: %+v", p)
	}
	for _, m := range []string{p.OpusModel, p.SonnetModel, p.HaikuModel, p.FableModel, p.SmallFastModel, p.SubagentModel} {
		if m != p.Model {
			t.Fatal("an auxiliary Claude model escaped the free model selection")
		}
	}
	t.Setenv("TIDEBOT_API_KEY", "unrelated-tidebot-key")
	t.Setenv("ANTHROPIC_API_KEY", "unrelated-anthropic-key")
	_ = a.Secrets.Set("provider:"+freepool.ProviderName, "old-provider-secret")
	key, err := a.ResolveAPIKey(p)
	if err != nil || len(key) != 64 || strings.Contains(key, "key") || key == "old-provider-secret" {
		t.Fatalf("managed provider did not use an isolated local token: %v", err)
	}
	statusJSON, _ := json.Marshal(a.freePoolStatus(ctx))
	viewsJSON, _ := json.Marshal((providerAdmin{a}).ListProviders(ctx))
	if strings.Contains(string(statusJSON), key) || strings.Contains(string(viewsJSON), key) || strings.Contains(string(viewsJSON), "old-provider-secret") {
		t.Fatal("status or provider list leaked a token")
	}
	if a.Cfg.DefaultProvider != defaultBefore {
		t.Fatal("installing a free pool changed the default provider")
	}
	if err := a.Plugins.SetEnabled(ctx, freepool.PluginID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Registry.Get(freepool.ProviderName); ok {
		t.Fatal("disabled provider remains registered")
	}
	if _, err := a.ResolveAPIKey(p); err == nil {
		t.Fatal("disabled profile still resolves a credential")
	}
	assertGatewayClosed(t, p.BaseURL)
	if err := a.Plugins.SetEnabled(ctx, freepool.PluginID, true); err != nil {
		t.Fatal(err)
	}
	p, err = a.ResolveProfile(freepool.ProviderName)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Plugins.SetPermission(ctx, freepool.PluginID, freePoolNetworkPermission, false); err != nil {
		t.Fatal(err)
	}
	assertGatewayClosed(t, p.BaseURL)
	if _, ok := a.Registry.Get(freepool.ProviderName); ok {
		t.Fatal("revoked provider remains registered")
	}
	if err := a.Plugins.SetPermission(ctx, freepool.PluginID, freePoolNetworkPermission, true); err != nil {
		t.Fatal(err)
	}
	p, _ = a.ResolveProfile(freepool.ProviderName)
	if err := a.Plugins.Uninstall(ctx, freepool.PluginID, false); err != nil {
		t.Fatal(err)
	}
	assertGatewayClosed(t, p.BaseURL)
	if _, ok := a.Registry.Get(freepool.ProviderName); ok || a.freePoolStatus(ctx)["ready"] != false {
		t.Fatal("uninstalled provider remains ready")
	}
}

func TestFreePoolConfigRevocationAndManagedGuards(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	installOfficial(t, a, freepool.PluginID)
	if _, ok := a.Registry.Get(freepool.ProviderName); ok {
		t.Fatal("training acknowledgement is required")
	}
	if err := a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"acknowledge_prompt_training": true}); err != nil {
		t.Fatal(err)
	}
	p, _ := a.ResolveProfile(freepool.ProviderName)
	pa := providerAdmin{a}
	for _, in := range []webui.ProviderInput{
		{Name: freepool.ProviderName, BaseURL: "https://example.com"},
		{Name: "renamed-free-pool", OriginalName: freepool.ProviderName, BaseURL: "https://example.com"},
	} {
		if _, err := pa.SaveProvider(ctx, in); !errors.Is(err, errManagedProvider) {
			t.Fatalf("managed provider edit permitted: %v", err)
		}
	}
	if err := pa.DeleteProvider(ctx, freepool.ProviderName); !errors.Is(err, errManagedProvider) {
		t.Fatalf("managed provider delete permitted: %v", err)
	}
	if _, err := a.setProviderPricing(ctx, pricingSetInput{Provider: freepool.ProviderName}); !errors.Is(err, errManagedProvider) {
		t.Fatalf("managed pricing edit permitted: %v", err)
	}
	if err := a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"model": "another:free", "acknowledge_prompt_training": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveAPIKey(p); err == nil {
		t.Fatal("queued profile retained credentials after a model change")
	}
	assertGatewayClosed(t, p.BaseURL)
	p, _ = a.ResolveProfile(freepool.ProviderName)
	if p.Model != "another:free" || p.HaikuModel != p.Model {
		t.Fatal("model update was not applied")
	}
	if err := a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"acknowledge_prompt_training": false}); err != nil {
		t.Fatal(err)
	}
	assertGatewayClosed(t, p.BaseURL)
	if _, err := a.ResolveProfile(freepool.ProviderName); err == nil {
		t.Fatal("revoking training acknowledgement left a provider available")
	}
}

func TestFreePoolPreservesCustomNameCollision(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	pa := providerAdmin{a}
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: freepool.ProviderName, BaseURL: "https://example.com", Model: "user-model"}); err != nil {
		t.Fatal(err)
	}
	installOfficial(t, a, freepool.PluginID)
	if err := a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"acknowledge_prompt_training": true}); err != nil {
		t.Fatal(err)
	}
	p, _ := a.Registry.Get(freepool.ProviderName)
	if p.Model != "user-model" || p.ManagedBy != "" || a.freePoolStatus(ctx)["ready"] != false {
		t.Fatal("existing custom provider overwritten")
	}
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "my-kilo", OriginalName: freepool.ProviderName, BaseURL: "https://example.com", Model: "user-model"}); err != nil {
		t.Fatalf("existing custom provider is locked: %v", err)
	}
	if err := a.Plugins.SetEnabled(ctx, freepool.PluginID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveProfile(freepool.ProviderName); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.Registry.Get("my-kilo"); p.Model != "user-model" {
		t.Fatal("renamed user profile was lost")
	}
}

func TestFreePoolRejectsForgedPluginAndRestoresAfterRestart(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	configureFreePool(t, a)
	pa := providerAdmin{a}
	if err := pa.SetDefaultProvider(ctx, freepool.ProviderName); err != nil {
		t.Fatal(err)
	}
	p, _ := a.ResolveProfile(freepool.ProviderName)
	oldKey, _ := a.ResolveAPIKey(p)
	var saved []provider.Profile
	a.loadJSONSetting(ctx, setProvidersUser, &saved)
	for _, row := range saved {
		if row.ManagedBy != "" || row.Name == freepool.ProviderName {
			t.Fatal("ephemeral managed profile was persisted")
		}
	}
	cfg := a.Cfg
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p2, err := b.ResolveProfile(freepool.ProviderName)
	if err != nil {
		t.Fatal(err)
	}
	newKey, _ := b.ResolveAPIKey(p2)
	if newKey == oldKey || b.Cfg.DefaultProvider != freepool.ProviderName {
		t.Fatal("restart did not rotate the local token or preserve an explicit default")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("id: kilo-free-pool\nname: forged\nversion: 9.9.9\npermissions:\n  network: [ai-gateway.kilo.ai]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := b.Plugins.Stage(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Plugins.Grant(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Registry.Get(freepool.ProviderName); ok {
		t.Fatal("local lookalike acquired a managed provider")
	}
	assertGatewayClosed(t, p2.BaseURL)
}

func TestFreePoolQueuedRunRechecksPermission(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	configureFreePool(t, a)
	a.Runner = runner.New(a.Store, a.Log, 1)
	entered, release, firstDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	firstProfile, workspace := mustProfile(t, a, "kimi-coding"), t.TempDir()
	go func() {
		defer close(firstDone)
		_, _ = a.Runner.RunOnce(ctx, runner.Options{Profile: firstProfile, WorkspaceDir: workspace,
			AtStart: func(ctx context.Context, _ *runner.Options) error {
				close(entered)
				select {
				case <-release:
					return errors.New("test slot released")
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not acquire a slot")
	}
	id := make(chan string, 1)
	done := make(chan runner.Result, 1)
	go func() {
		res, _ := a.RunPromptEx(ctx, runner.Options{Prompt: "do not start", WorkspaceDir: workspace, OnStart: func(s string) { id <- s }}, freepool.ProviderName, time.Minute)
		done <- res
	}()
	var runID string
	select {
	case runID = <-id:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("second run did not enter the queue")
	}
	waitFor(t, 5*time.Second, func() bool { st, _, _ := runRow(a, runID); return st == "queued" })
	if err := a.Plugins.SetEnabled(ctx, freepool.PluginID, false); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-firstDone
	select {
	case result := <-done:
		if result.Status != "skipped" {
			t.Fatalf("queued run launched after disable: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued run did not finish")
	}
}

func TestFreePoolRunRecordsFreeSourceAndUsage(t *testing.T) {
	a := newTestApp(t)
	configureFreePool(t, a)
	ctx := context.Background()
	result, err := a.RunPromptEx(ctx, runner.Options{Prompt: "test free usage accounting", WorkspaceDir: t.TempDir()}, freepool.ProviderName, time.Minute)
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("mock run: %+v %v", result, err)
	}
	if result.Cost.Source != pricing.SourceFree || result.Cost.Amount != 0 || result.Cost.Totals.Total() == 0 {
		t.Fatalf("free usage was not kept: %+v", result.Cost)
	}
	var source string
	var amount float64
	var tokens int
	if err := a.DB.QueryRowContext(ctx, `SELECT source, amount, input_tokens + output_tokens FROM cost_ledger WHERE run_id=?`, result.RunID).Scan(&source, &amount, &tokens); err != nil {
		t.Fatal(err)
	}
	if source != pricing.SourceFree || amount != 0 || tokens == 0 {
		t.Fatalf("bad ledger: source=%s amount=%v tokens=%d", source, amount, tokens)
	}
}

func TestFreePoolRechecksPermissionAtGateway(t *testing.T) {
	a := newTestApp(t)
	configureFreePool(t, a)
	ctx := context.Background()
	p, _ := a.ResolveProfile(freepool.ProviderName)
	key, _ := a.ResolveAPIKey(p)
	// Simulate a DB change outside the normal mutation hooks: every request must
	// still verify the current grant before a catalog or model request is sent.
	in, _ := a.Plugins.Get(ctx, freepool.PluginID)
	if _, err := a.DB.ExecContext(ctx, `UPDATE plugin_versions SET approved_permissions_json='[]' WHERE id=?`, in.Current.ID); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked request reached the upstream path: %d", response.StatusCode)
	}
	if _, err := a.ResolveProfile(freepool.ProviderName); err == nil {
		t.Fatal("stale registry bypassed the current permission check")
	}
}

func TestFreePoolVerifiesOfficialHashAfterVersionSwitch(t *testing.T) {
	a := newTestApp(t)
	configureFreePool(t, a)
	ctx := context.Background()
	in, _ := a.Plugins.Get(ctx, freepool.PluginID)
	approvedVersion := in.Current.ID
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("id: kilo-free-pool\nname: altered\nversion: 9.9.9\npermissions:\n  network: [ai-gateway.kilo.ai]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Runtime-free manifests can be staged by an internal caller with an official
	// source label; the capability must still require the embedded catalog hash.
	v, err := a.Plugins.StageSource(ctx, dir, "", plugin.SourceOfficial)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Plugins.Grant(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Registry.Get(freepool.ProviderName); ok {
		t.Fatal("unrecognized official-labelled version registered a provider")
	}
	if err := a.Plugins.SetCurrent(ctx, freepool.PluginID, approvedVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveProfile(freepool.ProviderName); err != nil {
		t.Fatalf("rollback did not restore the verified provider: %v", err)
	}
}

func TestFreePoolUnavailableDefaultDoesNotFallBackAfterRestart(t *testing.T) {
	for _, change := range []string{"disable", "revoke", "uninstall"} {
		t.Run(change, func(t *testing.T) {
			a := newTestApp(t)
			originalConfig := a.Cfg
			configureFreePool(t, a)
			ctx := context.Background()
			if err := (providerAdmin{a}).SetDefaultProvider(ctx, freepool.ProviderName); err != nil {
				t.Fatal(err)
			}
			var err error
			switch change {
			case "disable":
				err = a.Plugins.SetEnabled(ctx, freepool.PluginID, false)
			case "revoke":
				err = a.Plugins.SetPermission(ctx, freepool.PluginID, freePoolNetworkPermission, false)
			case "uninstall":
				err = a.Plugins.Uninstall(ctx, freepool.PluginID, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			b, err := Open(originalConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if b.Cfg.DefaultProvider != freepool.ProviderName {
				t.Fatalf("unavailable free default silently changed to %s", b.Cfg.DefaultProvider)
			}
			if p, err := b.ResolveProfile(""); err == nil {
				t.Fatalf("unavailable free default resolved to %+v", p)
			}
		})
	}
}

func TestFreePoolChildCannotSelectAnInheritedCloudBackend(t *testing.T) {
	a := newTestApp(t)
	configureFreePool(t, a)
	p, _ := a.ResolveProfile(freepool.ProviderName)
	key, _ := a.ResolveAPIKey(p)
	flags := []string{"CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_MANTLE", "CLAUDE_CODE_USE_VERTEX"}
	base := []string{"ANTHROPIC_API_KEY=unrelated-key", "ANTHROPIC_BASE_URL=https://example.com"}
	for _, flag := range flags {
		base = append(base, flag+"=1")
	}
	readEnv := func(profile provider.Profile) string {
		t.Helper()
		cmd := exec.Command("/usr/bin/env")
		cmd.Env = provider.ChildEnv(base, profile, key)
		b, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	managedEnv := readEnv(p)
	ordinaryEnv := readEnv(provider.Profile{Name: "ordinary"})
	for _, flag := range flags {
		if strings.Contains(managedEnv, flag+"=") {
			t.Fatalf("managed child inherited backend selector %s", flag)
		}
		if !strings.Contains(ordinaryEnv, flag+"=1") {
			t.Fatalf("ordinary provider backend selector changed: %s", flag)
		}
	}
	if !strings.Contains(managedEnv, "ANTHROPIC_BASE_URL="+p.BaseURL) || !strings.Contains(managedEnv, "ANTHROPIC_API_KEY="+key) ||
		!strings.Contains(managedEnv, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1") || strings.Contains(managedEnv, "unrelated-key") {
		t.Fatal("managed child did not receive only the host-selected route and token")
	}
}

func TestFreePoolUnchangedMutationsPreserveGateway(t *testing.T) {
	a := newTestApp(t)
	configureFreePool(t, a)
	installOfficial(t, a, "daily-digest")
	ctx := context.Background()
	in, _ := a.Plugins.Get(ctx, freepool.PluginID)
	p, _ := a.ResolveProfile(freepool.ProviderName)
	key, _ := a.ResolveAPIKey(p)
	a.freePool.mu.Lock()
	gateway := a.freePool.gateway
	a.freePool.mu.Unlock()
	steps := []func() error{
		func() error {
			return a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"acknowledge_prompt_training": true})
		},
		func() error {
			return a.Plugins.SetConfig(ctx, freepool.PluginID, map[string]any{"acknowledge_prompt_training": true, "model": freepool.DefaultModel})
		},
		func() error {
			return a.Plugins.SetConfig(ctx, "daily-digest", map[string]any{"feeds": "https://example.com/feed", "notify_via": "email", "hour": 7})
		},
		func() error { return a.Plugins.Grant(ctx, in.Current.ID, in.Current.Requested) },
		func() error { return a.Plugins.SetPermission(ctx, freepool.PluginID, freePoolNetworkPermission, true) },
		func() error { return a.Plugins.SetEnabled(ctx, freepool.PluginID, true) },
		func() error { return a.Plugins.SetCurrent(ctx, freepool.PluginID, in.Current.ID) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		a.freePool.mu.Lock()
		sameGateway := gateway == a.freePool.gateway
		a.freePool.mu.Unlock()
		currentKey, err := a.ResolveAPIKey(p)
		if !sameGateway || err != nil || currentKey != key {
			t.Fatalf("unchanged operation %d replaced the active gateway", i)
		}
		res, err := (&http.Client{Timeout: time.Second}).Get(p.BaseURL + "/v1/models")
		if err != nil {
			t.Fatalf("unchanged operation %d closed the listener: %v", i, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatal("local gateway stopped enforcing its token")
		}
	}
}
