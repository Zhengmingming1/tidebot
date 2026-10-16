package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
)

// officialRPC: built-in plugin catalog, connector tests, trigger previews, pack apply.
func (a *App) officialRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"plugins.catalog": func(ctx context.Context, _ json.RawMessage) (any, error) {
			list, err := official.List()
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, e := range list {
				row := map[string]any{"id": e.ID, "category": e.Category, "icon": e.Icon, "name": e.Manifest.Name, "version": e.Manifest.Version,
					"permissions": e.Manifest.Permissions.PermissionList(), "triggers": e.Manifest.Contributes.Triggers, "connectors": len(e.Manifest.Contributes.Connectors)}
				if in, err := a.Plugins.Get(ctx, e.ID); err == nil {
					row["installed"] = in.Current != nil
					row["source"] = in.Source
					if in.Current != nil {
						row["installed_version"] = in.Current.Version
						row["update"] = in.Source == plugin.SourceOfficial && in.Current.ContentHash != e.Hash
					}
				}
				out = append(out, row)
			}
			return out, nil
		},
		// Step 1 of a one-click install: stage the embedded plugin. Step 2 is the usual
		// plugins.approve with the items the owner ticked; nothing is granted here.
		"plugins.install_official": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			v, err := official.Stage(ctx, a.Plugins, p.ID)
			if err != nil {
				return nil, err
			}
			newPerms := v.Requested
			if in, err := a.Plugins.Get(ctx, p.ID); err == nil && in.Current != nil {
				newPerms = newPermsOf(in.Current.Approved, v.Requested)
			}
			if newPerms == nil {
				newPerms = []string{}
			}
			return map[string]any{"version": v, "new_permissions": newPerms}, nil
		},
		"plugins.test": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				ID     string         `json:"id"`
				Config map[string]any `json:"config"`
			}](raw)
			res, err := a.PluginHost.Call(p.ID, "plugin.test", map[string]any{"config": p.Config})
			if err != nil {
				return nil, err
			}
			var out any
			_ = json.Unmarshal(res, &out)
			return out, nil
		},
		"triggers.preview": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				PluginID string         `json:"plugin_id"`
				Config   map[string]any `json:"config"`
			}](raw)
			res, err := a.PluginHost.Call(p.PluginID, "trigger.preview", map[string]any{"config": p.Config})
			if err != nil {
				return nil, err
			}
			var out any
			_ = json.Unmarshal(res, &out)
			return out, nil
		},
		"plugins.trigger_fields": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				PluginID  string `json:"plugin_id"`
				TriggerID string `json:"trigger_id"`
			}](raw)
			in, err := a.Plugins.Get(ctx, p.PluginID)
			if err != nil || in.Current == nil {
				return nil, errors.New("plugin not installed")
			}
			for _, t := range in.Current.Manifest.Contributes.Triggers {
				if t.ID == p.TriggerID {
					if t.ConfigSchema == nil {
						return []plugin.Field{}, nil
					}
					return plugin.ParseSchema(t.ConfigSchema)
				}
			}
			return nil, errors.New("no such trigger")
		},
		"plugins.apply_pack": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			return a.applyDigestPack(ctx, p.ID)
		},
	}
}

// digestSenders maps notify_via to (plugin, connector, tool).
var digestSenders = map[string][3]string{
	"wecom":    {"wecom", "wecom", "send_markdown"},
	"dingtalk": {"dingtalk", "dingtalk", "send_markdown"},
	"email":    {"email", "email", "send_email"},
}

func mcpTool(pluginID, connector, tool string) string {
	return "mcp__" + pluginID + "__" + connector + "__" + tool
}

// applyDigestPack turns the daily-digest plugin's settings into a skill, a paused daily
// task and a home-screen button. The task only gets the MCP tools it needs.
func (a *App) applyDigestPack(ctx context.Context, id string) (map[string]any, error) {
	if id != "daily-digest" {
		return nil, errors.New("only the daily-digest pack can be applied")
	}
	in, err := a.Plugins.Get(ctx, id)
	if err != nil || in.Current == nil || in.Source != plugin.SourceOfficial {
		return nil, errors.New("install the daily digest pack first")
	}
	cfg := a.Plugins.ActivationConfig(ctx, id)
	var feeds []string
	for _, l := range strings.FieldsFunc(fmt.Sprint(cfg["feeds"]), func(r rune) bool { return r == '\n' || r == ',' || r == ' ' }) {
		if u, err := url.Parse(strings.TrimSpace(l)); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			feeds = append(feeds, u.String())
		}
	}
	if len(feeds) == 0 {
		return nil, errors.New("add at least one feed URL in the pack settings")
	}
	via, _ := cfg["notify_via"].(string)
	snd, ok := digestSenders[via]
	if !ok {
		return nil, errors.New("choose WeCom, DingTalk or email for the digest")
	}
	var missing []string
	for _, need := range []string{"rss-feed", snd[0]} {
		if pi, err := a.Plugins.Get(ctx, need); err != nil || pi.Current == nil || !pi.Enabled {
			missing = append(missing, need)
		}
	}
	hour := 8
	if h, ok := cfg["hour"].(float64); ok && h >= 0 && h <= 23 {
		hour = int(h)
	}
	topics, _ := cfg["topics"].(string)
	prompt := "Use the daily-digest skill.\n\nFeeds:\n- " + strings.Join(feeds, "\n- ") +
		"\n\nTopics: " + firstNonEmptyStr(topics, "(any)") +
		"\n\nFetch with " + mcpTool("rss-feed", "feeds", "fetch_feed") + " (since_hours: 24) and send the digest with " + mcpTool(snd[0], snd[1], snd[2]) + "."
	files, err := official.PackFiles(id)
	if err != nil {
		return nil, err
	}
	pk := Pack{Format: PackFormat, Name: "每日资讯摘要", CreatedAt: time.Now().UTC().Format(time.RFC3339), Skills: []string{"daily-digest"},
		Tasks: []PackTask{{Name: "每日资讯摘要", Goal: "Daily news digest from RSS feeds", Prompt: prompt, CronExpr: fmt.Sprintf("0 %d * * *", hour),
			OverlapPolicy: "skip", TimeoutMinutes: 15, Skills: []string{"daily-digest"},
			AllowedTools: []string{mcpTool("rss-feed", "feeds", "fetch_feed"), mcpTool(snd[0], snd[1], snd[2])}}}}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	pj, _ := json.MarshalIndent(pk, "", "  ")
	w, _ := zw.Create("pack.json")
	_, _ = w.Write(pj)
	for name, b := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write(b)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "digest-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.Write(buf.Bytes())
	tmp.Close()
	res, err := a.ImportPack(ctx, tmp.Name())
	if err != nil {
		return nil, err
	}
	out := map[string]any{"task_ids": res.TaskIDs, "missing_plugins": missing, "duplicate": res.Duplicate}
	if len(res.TaskIDs) > 0 && !res.Duplicate {
		if err := a.addButton(ctx, map[string]any{"label": "每日资讯摘要", "icon": "book", "task_id": res.TaskIDs[0]}); err == nil {
			out["button"] = true
		}
	}
	return out, nil
}

func firstNonEmptyStr(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// addButton appends a home-screen button (same storage as the WebUI's button editor).
func (a *App) addButton(ctx context.Context, b map[string]any) error {
	raw, _ := a.Auth.GetSetting(ctx, "buttons")
	var list []map[string]any
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	taken := map[string]bool{}
	for _, x := range list {
		if s, _ := x["id"].(string); s != "" {
			taken[s] = true
		}
	}
	idv := "daily-digest"
	for i := 2; taken[idv]; i++ {
		idv = fmt.Sprintf("daily-digest-%d", i)
	}
	b["id"] = idv
	list = append(list, b)
	bs, _ := json.Marshal(list)
	return a.Auth.SetSetting(ctx, "buttons", string(bs))
}
