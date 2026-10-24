package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

type ctxKey string

const externalInputKey ctxKey = "external_input"

// EventPluginNotify / EventPluginBreaker are emitted for plugin activity.
const (
	EventPluginNotify  = "plugin_notify"
	EventPluginBreaker = "plugin_breaker"
)

// TriggerBinding connects an external event source to a task.
type TriggerBinding struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"` // webhook | file_change | email | im
	PluginID  string         `json:"plugin_id,omitempty"`
	TaskID    string         `json:"task_id"`
	Config    map[string]any `json:"config"`
	Enabled   bool           `json:"enabled"`
	CreatedAt string         `json:"created_at"`
	// LastFiredAt / LastResult / FireCount feed the trigger cards ("accepted", "rate limited", ...).
	LastFiredAt string `json:"last_fired_at,omitempty"`
	LastResult  string `json:"last_result,omitempty"`
	FireCount   int    `json:"fire_count"`
}

// triggerMinInterval rate-limits each binding.
const triggerMinInterval = 5 * time.Second

type triggerState struct {
	mu       sync.Mutex
	lastFire map[string]time.Time
	snaps    map[string]map[string]string
}

func (a *App) initPlugins() {
	a.Plugins = &plugin.Store{DB: a.DB, Root: filepath.Join(a.Cfg.HomeDir, "plugins"), Secrets: a.Secrets}
	a.Plugins.OnUninstall = a.pauseDependents
	a.PluginHost = plugin.NewHost(a.Plugins, pluginAPI{a}, a.Log)
	if plugin.BuiltinExe == "" {
		if exe, err := os.Executable(); err == nil {
			plugin.BuiltinExe = exe
		}
	}
	a.trig = &triggerState{lastFire: map[string]time.Time{}, snaps: map[string]map[string]string{}}
}

// pauseDependents pauses every task bound to the plugin's triggers before it goes away.
func (a *App) pauseDependents(ctx context.Context, pluginID string) {
	a.PluginHost.Stop(pluginID)
	rows, err := a.DB.QueryContext(ctx, `SELECT DISTINCT task_id FROM trigger_bindings WHERE plugin_id=? AND task_id IS NOT NULL`, pluginID)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var t string
		_ = rows.Scan(&t)
		ids = append(ids, t)
	}
	rows.Close()
	for _, t := range ids {
		if err := a.Tasks.Pause(ctx, t, tasksvc.PausePlugin, pluginID); err == nil {
			a.Log.Info("paused task: plugin uninstalled", "task_id", t, "plugin", pluginID)
		}
	}
}

// mcpArgs writes the plugin connector config for a run (sandbox drops side-effect connectors).
func (a *App) mcpArgs(ctx context.Context, bucket string) []string {
	if a.Plugins == nil {
		return nil
	}
	sandbox := bucket == cost.BucketDryRun
	b, n, err := a.Plugins.MCPConfig(ctx, sandbox)
	if err != nil || n == 0 {
		return nil
	}
	name := "mcp-plugins.json"
	if sandbox {
		name = "mcp-plugins-sandbox.json"
	}
	p := filepath.Join(a.Cfg.DataDir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return nil
	}
	return []string{"--mcp-config", p}
}

// fireTask starts a task for an external event with the payload wrapped as untrusted data.
func (a *App) fireTask(bindingID, taskID, kind, source, payload string) (err error) {
	defer func() {
		res := "accepted"
		if err != nil {
			res = err.Error()
		}
		_, _ = a.DB.Exec(`UPDATE trigger_bindings SET last_fired_at=?, last_result=?, fire_count=fire_count+1 WHERE id=?`, time.Now().UTC().Format(time.RFC3339), res, bindingID)
	}()
	a.trig.mu.Lock()
	if t, ok := a.trig.lastFire[bindingID]; ok && time.Since(t) < triggerMinInterval {
		a.trig.mu.Unlock()
		return errors.New("rate limited")
	}
	a.trig.lastFire[bindingID] = time.Now()
	a.trig.mu.Unlock()
	task, err := a.Store.GetTask(context.Background(), taskID)
	if err != nil {
		return errors.New("task not found")
	}
	if task.Status != "active" {
		return errors.New("task is not active")
	}
	if len(payload) > 64<<10 {
		payload = payload[:64<<10] + "\n[truncated]"
	}
	ctx := context.WithValue(context.Background(), externalInputKey, guard.WrapExternal(source, payload))
	go func() {
		if err := a.runTaskTrigger(ctx, *task, kind, cost.BucketProduction); err != nil {
			a.Log.Warn("triggered run", "task_id", taskID, "kind", kind, "error", err)
		}
	}()
	return nil
}

// externalPrompt adds the wrapped payload (if any) to a compiled prompt.
func externalPrompt(ctx context.Context, prompt string) string {
	if v, ok := ctx.Value(externalInputKey).(string); ok && v != "" {
		return guard.ExternalPreamble + prompt + "\n\n" + v
	}
	return prompt
}

// --- triggers -------------------------------------------------------------

func (a *App) listTriggers(ctx context.Context) ([]TriggerBinding, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, kind, COALESCE(plugin_id,''), COALESCE(task_id,''), config_json, enabled, created_at, COALESCE(last_fired_at,''), last_result, fire_count FROM trigger_bindings ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TriggerBinding{}
	for rows.Next() {
		var b TriggerBinding
		var cj string
		var en int
		if err := rows.Scan(&b.ID, &b.Kind, &b.PluginID, &b.TaskID, &cj, &en, &b.CreatedAt, &b.LastFiredAt, &b.LastResult, &b.FireCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cj), &b.Config)
		b.Enabled = en == 1
		out = append(out, b)
	}
	return out, rows.Err()
}

func (a *App) getTrigger(ctx context.Context, bid string) (*TriggerBinding, error) {
	list, err := a.listTriggers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == bid {
			return &list[i], nil
		}
	}
	return nil, errors.New("trigger not found")
}

// createTrigger validates and stores a binding. For webhooks it returns the token once.
func (a *App) createTrigger(ctx context.Context, in TriggerBinding) (map[string]any, error) {
	if _, err := a.Store.GetTask(ctx, in.TaskID); err != nil {
		return nil, errors.New("task not found")
	}
	if in.Config == nil {
		in.Config = map[string]any{}
	}
	switch in.Kind {
	case "webhook":
	case "file_change":
		p, _ := in.Config["path"].(string)
		if p == "" || !filepath.IsAbs(p) {
			return nil, errors.New("file_change needs an absolute config.path")
		}
	case "email", "im", "rss", "web_monitor", "poll":
		if in.PluginID == "" {
			return nil, errors.New(in.Kind + " triggers come from a plugin; set plugin_id")
		}
		pi, err := a.Plugins.Get(ctx, in.PluginID)
		if err != nil || pi.Current == nil {
			return nil, errors.New("plugin not installed")
		}
		tid, _ := in.Config["trigger_id"].(string)
		ok := false
		for _, t := range pi.Current.Manifest.Contributes.Triggers {
			if t.ID == tid && t.Kind == in.Kind {
				ok = true
				if t.ConfigSchema != nil {
					fields, err := plugin.ParseSchema(t.ConfigSchema)
					if err != nil {
						return nil, err
					}
					vals := map[string]any{}
					for k, v := range in.Config {
						if k != "trigger_id" {
							vals[k] = v
						}
					}
					plain, sec, err := plugin.ValidateConfig(fields, vals)
					if err != nil {
						return nil, err
					}
					if len(sec) > 0 {
						return nil, errors.New("trigger settings cannot hold secrets; put them in the plugin settings")
					}
					plain["trigger_id"] = tid
					in.Config = plain
				}
			}
		}
		if !ok {
			return nil, fmt.Errorf("plugin %s has no %s trigger %q", in.PluginID, in.Kind, tid)
		}
	default:
		return nil, errors.New("kind must be webhook, file_change, email, im, rss, web_monitor or poll")
	}
	in.ID = id.New("trg")
	res := map[string]any{"id": in.ID}
	secretHash := ""
	if in.Kind == "webhook" {
		buf := make([]byte, 24)
		_, _ = rand.Read(buf)
		tok := hex.EncodeToString(buf)
		if err := a.Secrets.Set("trigger."+in.ID, tok); err != nil {
			return nil, err
		}
		h := sha256.Sum256([]byte(tok))
		secretHash = hex.EncodeToString(h[:])
		res["token"] = tok
		res["url_path"] = "/hooks/" + in.ID
	}
	cj, _ := json.Marshal(in.Config)
	var pid any
	if in.PluginID != "" {
		pid = in.PluginID
	}
	_, err := a.DB.ExecContext(ctx, `INSERT INTO trigger_bindings(id, kind, plugin_id, task_id, config_json, secret_hash, enabled, created_at) VALUES(?,?,?,?,?,?,1,?)`,
		in.ID, in.Kind, pid, in.TaskID, string(cj), secretHash, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	if in.PluginID != "" {
		_, _ = a.PluginHost.Call(in.PluginID, "trigger.subscribe", map[string]any{"binding_id": in.ID, "trigger_id": in.Config["trigger_id"], "config": in.Config})
	}
	return res, nil
}

func (a *App) deleteTrigger(ctx context.Context, bid string) error {
	if b, err := a.getTrigger(ctx, bid); err == nil && b.PluginID != "" && a.PluginHost.Running(b.PluginID) {
		_, _ = a.PluginHost.Call(b.PluginID, "trigger.unsubscribe", map[string]any{"binding_id": bid})
	}
	_ = a.Secrets.Delete("trigger." + bid)
	_, err := a.DB.ExecContext(ctx, `DELETE FROM trigger_bindings WHERE id=?`, bid)
	return err
}

// WebhookHandler serves POST /hooks/<id>. Auth: "Authorization: Bearer <token>" or
// "X-Tidebot-Signature: sha256=<hex HMAC-SHA256(body, token)>".
func (a *App) WebhookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		bid := strings.TrimPrefix(r.URL.Path, "/hooks/")
		b, err := a.getTrigger(r.Context(), bid)
		if err != nil || b.Kind != "webhook" || !b.Enabled {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		tok, err := a.Secrets.Get("trigger." + bid)
		if err != nil || tok == "" || !webhookAuthorized(r, body, tok) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := a.fireTask(bid, b.TaskID, "webhook", "webhook:"+bid, string(body)); err != nil {
			code := http.StatusConflict
			if err.Error() == "rate limited" {
				code = http.StatusTooManyRequests
			}
			http.Error(w, err.Error(), code)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	})
}

func webhookAuthorized(r *http.Request, body []byte, tok string) bool {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(tok)) == 1
	}
	if sig := r.Header.Get("X-Tidebot-Signature"); strings.HasPrefix(sig, "sha256=") {
		m := hmac.New(sha256.New, []byte(tok))
		m.Write(body)
		want := hex.EncodeToString(m.Sum(nil))
		return hmac.Equal([]byte(strings.TrimPrefix(sig, "sha256=")), []byte(want))
	}
	return false
}

// runFileWatcher polls file_change bindings (default every 30 s) and fires on changes.
func (a *App) runFileWatcher(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		a.scanFileTriggers(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) scanFileTriggers(ctx context.Context) {
	list, err := a.listTriggers(ctx)
	if err != nil {
		return
	}
	for _, b := range list {
		if b.Kind != "file_change" || !b.Enabled {
			continue
		}
		root, _ := b.Config["path"].(string)
		cur := snapshot(root, 5000)
		a.trig.mu.Lock()
		prev, seen := a.trig.snaps[b.ID]
		a.trig.snaps[b.ID] = cur
		a.trig.mu.Unlock()
		if !seen {
			continue
		}
		var changed []string
		for p, sig := range cur {
			if prev[p] != sig {
				changed = append(changed, "modified: "+p)
			}
		}
		for p := range prev {
			if _, ok := cur[p]; !ok {
				changed = append(changed, "deleted: "+p)
			}
		}
		if len(changed) == 0 {
			continue
		}
		if len(changed) > 200 {
			changed = append(changed[:200], fmt.Sprintf("... and %d more", len(changed)-200))
		}
		if err := a.fireTask(b.ID, b.TaskID, "file_change", "file_change:"+root, strings.Join(changed, "\n")); err != nil {
			a.Log.Warn("file trigger", "trigger", b.ID, "error", err)
		}
	}
}

func snapshot(root string, max int) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(out) >= max {
			return filepath.SkipDir
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				out[p] = fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
			}
		}
		return nil
	})
	return out
}

// --- plugin host callbacks -----------------------------------------------

type pluginAPI struct{ a *App }

func (p pluginAPI) TriggerFire(ctx context.Context, pluginID, triggerID string, payload json.RawMessage) error {
	list, err := p.a.listTriggers(ctx)
	if err != nil {
		return err
	}
	fired := 0
	for _, b := range list {
		if b.PluginID != pluginID || !b.Enabled {
			continue
		}
		if tid, _ := b.Config["trigger_id"].(string); tid != triggerID && b.ID != triggerID {
			continue
		}
		if err := p.a.fireTask(b.ID, b.TaskID, b.Kind, pluginID+":"+triggerID, string(payload)); err == nil {
			fired++
		}
	}
	if fired == 0 {
		return errors.New("no active binding for this trigger")
	}
	return nil
}

func (p pluginAPI) RunsStart(ctx context.Context, pluginID, target string, vars map[string]string) (string, error) {
	var n int
	_ = p.a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM trigger_bindings WHERE plugin_id=? AND task_id=?`, pluginID, target).Scan(&n)
	if n == 0 {
		return "", errors.New("plugin is not bound to this task")
	}
	vb, _ := json.Marshal(vars)
	return "", p.a.fireTask("plugin-run:"+pluginID+":"+target, target, "plugin", pluginID, string(vb))
}

func (p pluginAPI) NotifySend(ctx context.Context, pluginID, title, body string) error {
	p.a.Events.Emit(Event{Kind: EventPluginNotify, TaskName: pluginID, Detail: title + ": " + body})
	return nil
}

func (p pluginAPI) TasksRead(ctx context.Context, pluginID string) (any, error) {
	rows, err := p.a.DB.QueryContext(ctx, `SELECT t.id, t.name, t.status FROM tasks t JOIN trigger_bindings b ON b.task_id=t.id WHERE b.plugin_id=?`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var i, n, s string
		_ = rows.Scan(&i, &n, &s)
		out = append(out, map[string]string{"id": i, "name": n, "status": s})
	}
	return out, nil
}

func (p pluginAPI) PluginEvent(pluginID, kind, detail string) {
	p.a.Events.Emit(Event{Kind: kind, TaskName: pluginID, Detail: detail})
}

var _ = sql.ErrNoRows
