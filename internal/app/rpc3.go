package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

type rpcFunc = func(context.Context, json.RawMessage) (any, error)

// extRPC returns the plugin/trigger/pack/system methods. They are served over IPC and,
// for the owner's WebUI session, through /api/rpc/<method>.
func (a *App) extRPC() map[string]rpcFunc {
	m := map[string]rpcFunc{
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
			if err := a.Plugins.Approve(ctx, p.VersionID, p.Permissions); err != nil {
				return nil, err
			}
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
	for k, v := range a.systemRPC() {
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
