package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/i18n"
	"github.com/Zhengmingming1/tidebot/internal/ipc"
	"github.com/Zhengmingming1/tidebot/internal/notify"
)

type rpcFunc = func(context.Context, json.RawMessage) (any, error)

// extRPC returns the plugin/trigger/pack/system methods. They are served over IPC and,
// for the owner's WebUI session, through /api/rpc/<method>.
func (a *App) extRPC() map[string]rpcFunc {
	m := map[string]rpcFunc{
		// Settings page: channels without URLs or secrets (robot URLs carry the access token).
		"notify.overview": func(ctx context.Context, _ json.RawMessage) (any, error) {
			list, err := a.Notify.Summaries(ctx)
			if err != nil {
				return nil, err
			}
			groups := []string{}
			for _, g := range notify.EventGroups {
				groups = append(groups, g.ID)
			}
			q, _ := a.settingString(ctx, "notify.quiet")
			h, _ := a.settingString(ctx, "notify.digest_hour")
			return map[string]any{"channels": list, "quiet": q, "digest_hour": h, "groups": groups, "defaults": notify.DefaultGroups}, nil
		},
		// Add / edit a channel from the WebUI; URL and secret are write-only (blank keeps).
		"notify.channel_save": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, err := decode[struct {
				ID string `json:"id"`
				notify.Input
			}](raw)
			if err != nil {
				return nil, err
			}
			c, err := a.Notify.Save(ctx, p.ID, p.Input)
			if err != nil {
				return nil, err
			}
			return map[string]string{"id": c.ID}, nil
		},
		"notify.channel_enable": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			}](raw)
			return map[string]bool{"ok": true}, a.Notify.SetEnabled(ctx, p.ID, p.Enabled)
		},
		// Test what is in the form (saved or not); blank URL / secret use the stored ones.
		"notify.channel_test": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, err := decode[struct {
				ID string `json:"id"`
				notify.Input
			}](raw)
			if err != nil {
				return nil, err
			}
			a.syncLang(ctx)
			m := notify.Message{Event: "test", Key: "test", Title: i18n.T("notify.test"), Body: i18n.T("notify.test_body")}
			if err := a.Notify.Test(ctx, p.ID, p.Input, m); err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		},
		"notify.channel_remove": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID string `json:"id"`
			}](raw)
			if p.ID == "" {
				return nil, errors.New("id required")
			}
			return map[string]bool{"ok": true}, a.Notify.RemoveChannel(ctx, p.ID)
		},
		"notify.settings": a.rpcNotifySettings,
		"backups.list":    func(ctx context.Context, _ json.RawMessage) (any, error) { return a.ListBackups(ctx) },
		// WebUI backups never include secrets (no passphrase over the wire).
		"backups.create": func(ctx context.Context, _ json.RawMessage) (any, error) {
			p, err := a.CreateBackup(ctx, BackupOptions{Kind: "manual"})
			if err != nil {
				return nil, err
			}
			return map[string]string{"path": p}, nil
		},
		"plugins.list": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.Plugins.List(ctx) },
		"plugins.get": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			in, err := a.Plugins.Get(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"plugin": in, "running": a.PluginHost.Running(p.ID), "breaker_open": a.PluginHost.BreakerOpen(p.ID)}, nil
		},
		// stage = install or upgrade step 1; the response lists permissions (and new ones vs current)
		"plugins.stage": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				Dir  string `json:"dir"`
				Hash string `json:"hash"`
			}](raw)
			if p.Dir == "" {
				return nil, errors.New("dir required")
			}
			v, err := a.Plugins.Stage(ctx, p.Dir, p.Hash)
			if err != nil {
				return nil, err
			}
			var newPerms []string
			if in, err := a.Plugins.Get(ctx, v.Manifest.ID); err == nil && in.Current != nil {
				newPerms = newPermsOf(in.Current.Approved, v.Requested)
			} else {
				newPerms = v.Requested
			}
			return map[string]any{"version": v, "new_permissions": newPerms}, nil
		},
		"plugins.approve": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				VersionID   string   `json:"version_id"`
				Permissions []string `json:"permissions"`
			}](raw)
			// Consent is per item: anything not listed is declined and stays off.
			if err := a.Plugins.Grant(ctx, p.VersionID, p.Permissions); err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		},
		"plugins.permission": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID         string `json:"id"`
				Permission string `json:"permission"`
				Granted    bool   `json:"granted"`
			}](raw)
			if err := a.Plugins.SetPermission(ctx, p.ID, p.Permission, p.Granted); err != nil {
				return nil, err
			}
			a.PluginHost.Stop(p.ID) // restart with the new grant set on next use
			return map[string]bool{"ok": true}, nil
		},
		"plugins.config_get": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			return a.Plugins.GetConfig(ctx, p.ID)
		},
		"plugins.config_set": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID     string         `json:"id"`
				Values map[string]any `json:"values"`
			}](raw)
			if err := a.Plugins.SetConfig(ctx, p.ID, p.Values); err != nil {
				return nil, err
			}
			a.PluginHost.Stop(p.ID)
			return map[string]bool{"ok": true}, nil
		},
		"plugins.rollback": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID        string `json:"id"`
				VersionID string `json:"version_id"`
			}](raw)
			a.PluginHost.Stop(p.ID)
			return nil, a.Plugins.SetCurrent(ctx, p.ID, p.VersionID)
		},
		"plugins.enable": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			}](raw)
			if !p.Enabled {
				a.PluginHost.Stop(p.ID)
			}
			v := 0
			if p.Enabled {
				v = 1
			}
			_, err := a.DB.ExecContext(ctx, `UPDATE plugins SET enabled=? WHERE id=?`, v, p.ID)
			return nil, err
		},
		"plugins.uninstall": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID       string `json:"id"`
				KeepData bool   `json:"keep_data"`
			}](raw)
			return nil, a.Plugins.Uninstall(ctx, p.ID, p.KeepData)
		},
		"plugins.set_secret": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Value string `json:"value"`
			}](raw)
			return nil, a.Plugins.SetSecret(ctx, p.ID, p.Name, p.Value)
		},
		"plugins.reset_breaker": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			a.PluginHost.ResetBreaker(p.ID)
			return nil, nil
		},
		"triggers.list": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.listTriggers(ctx) },
		"triggers.create": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[TriggerBinding](raw)
			if err != nil {
				return nil, err
			}
			return a.createTrigger(ctx, in)
		},
		"triggers.enable": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			}](raw)
			v := 0
			if p.Enabled {
				v = 1
			}
			res, err := a.DB.ExecContext(ctx, `UPDATE trigger_bindings SET enabled=? WHERE id=?`, v, p.ID)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, errors.New("trigger not found")
			}
			return map[string]bool{"ok": true}, nil
		},
		// triggers.test fires the binding with a sample payload (marked as a test) so the run
		// shows up in run history. Same rate limit and "task must be active" rule as real events.
		"triggers.test": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			b, err := a.getTrigger(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			if !b.Enabled {
				return nil, errors.New("trigger is disabled")
			}
			payload := `{"test":true,"note":"test event sent from tidebot"}`
			if err := a.fireTask(b.ID, b.TaskID, b.Kind, b.Kind+"-test:"+b.ID, payload); err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		},
		"triggers.delete": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			return nil, a.deleteTrigger(ctx, p.ID)
		},
		"packs.export": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				Name    string   `json:"name"`
				TaskIDs []string `json:"task_ids"`
				Out     string   `json:"out"`
			}](raw)
			if p.Out == "" || len(p.TaskIDs) == 0 {
				return nil, errors.New("out and task_ids required")
			}
			h, err := a.ExportPack(ctx, p.Name, p.TaskIDs, p.Out)
			return map[string]string{"path": p.Out, "sha256": h}, err
		},
		"packs.import": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ File string }](raw)
			return a.ImportPack(ctx, p.File)
		},
	}
	for k, v := range a.officialRPC() {
		m[k] = v
	}
	for k, v := range a.priceCatalogRPC() {
		m[k] = v
	}
	for k, v := range a.pricingRPC() {
		m[k] = v
	}
	for k, v := range a.networkRPC() {
		m[k] = v
	}
	for k, v := range a.pluginAIRPC() {
		m[k] = v
	}
	for k, v := range a.systemRPC() {
		m[k] = v
	}
	for k, v := range a.storageRPC() {
		m[k] = v
	}
	for k, v := range a.powerRPC() {
		m[k] = v
	}
	for k, v := range a.timezoneRPC() {
		m[k] = v
	}
	for k, v := range a.schedPauseRPC() {
		m[k] = v
	}
	// Skill optimizer (review / canary) is reachable from the WebUI too.
	a.registerSkillRPC(func(n string, h func(context.Context, json.RawMessage) (any, error)) { m[n] = h })
	return m
}

func newPermsOf(prev, next []string) []string {
	have := map[string]bool{}
	for _, p := range prev {
		have[p] = true
	}
	out := []string{}
	for _, p := range next {
		if !have[p] {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) registerRPC3(srv *ipc.Server) {
	for k, v := range a.extRPC() {
		srv.Register(k, v)
	}
}

// CallExt runs one ext method (WebUI bridge).
func (a *App) CallExt(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	f, ok := a.extRPC()[method]
	if !ok {
		return nil, errors.New("unknown method")
	}
	return f(ctx, raw)
}
