package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
)

// The test binary doubles as the builtin plugin host: `app.test __plugin rss plugin`.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == plugin.BuiltinArg {
		os.Exit(official.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func installOfficial(t *testing.T, a *App, id string) {
	t.Helper()
	ctx := context.Background()
	raw, err := a.CallExt(ctx, "plugins.install_official", json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	v := raw.(map[string]any)["version"].(*plugin.VersionInfo)
	in, _ := a.Plugins.Get(ctx, id)
	if in.Current != nil {
		t.Fatal("staging must not activate anything before approval")
	}
	if err := a.Plugins.Grant(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
}

func TestOfficialCatalogInstallAndRSSTrigger(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	feed := `<rss version="2.0"><channel><title>F</title><item><title>old</title><guid>1</guid></item></channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feed) }))
	defer srv.Close()

	cat, err := a.CallExt(ctx, "plugins.catalog", nil)
	if err != nil || len(cat.([]map[string]any)) != len(official.Order) {
		t.Fatalf("catalog %v %v", cat, err)
	}
	// A look-alike that is not the embedded package is refused even if it claims official.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("id: rss-feed\nname: x\nversion: 1.0.0\nruntime: {builtin: rss}\n"), 0o644)
	if _, err := a.Plugins.StageSource(ctx, dir, "", plugin.SourceOfficial); err == nil {
		t.Fatal("forged builtin accepted")
	}
	if _, err := a.Plugins.Stage(ctx, dir, ""); err == nil {
		t.Fatal("local plugin may not use builtin runtime")
	}

	installOfficial(t, a, "rss-feed")
	in, _ := a.Plugins.Get(ctx, "rss-feed")
	if in.Source != plugin.SourceOfficial || in.Current == nil {
		t.Fatalf("%+v", in)
	}
	defer a.PluginHost.StopAll()
	tid := mkTask(t, a, "on-feed")
	if _, err := a.createTrigger(ctx, TriggerBinding{Kind: "rss", PluginID: "rss-feed", TaskID: tid, Config: map[string]any{"trigger_id": "new_item", "url": srv.URL, "interval_minutes": 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createTrigger(ctx, TriggerBinding{Kind: "rss", PluginID: "rss-feed", TaskID: tid, Config: map[string]any{"trigger_id": "new_item", "interval_minutes": 1}}); err == nil {
		t.Fatal("trigger config is validated against the schema (url required, min interval)")
	}
	prev, err := a.CallExt(ctx, "triggers.preview", json.RawMessage(`{"plugin_id":"rss-feed","config":{"url":"`+srv.URL+`"}}`))
	if err != nil || !strings.Contains(toJSONStr(prev), `"old"`) {
		t.Fatalf("preview %v %v", prev, err)
	}
	// The MCP config points the connector at our own binary.
	args, _ := a.mcpArgs(ctx, "production", nil, nil)
	b, _ := os.ReadFile(args[1])
	if !strings.Contains(string(b), `"__plugin"`) || !strings.Contains(string(b), "rss-feed__feeds") {
		t.Fatalf("mcp %s", b)
	}
	time.Sleep(300 * time.Millisecond)
	if runCount(a, tid) != 0 {
		t.Fatal("first poll must only seed")
	}
}

func TestDigestPackApply(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"rss-feed", "wecom", "daily-digest"} {
		installOfficial(t, a, id)
	}
	defer a.PluginHost.StopAll()
	if _, err := a.CallExt(ctx, "plugins.apply_pack", json.RawMessage(`{"id":"daily-digest"}`)); err == nil {
		t.Fatal("needs feeds first")
	}
	if err := a.Plugins.SetConfig(ctx, "daily-digest", map[string]any{"feeds": "https://ex.com/a.xml\nnot a url", "notify_via": "wecom", "hour": 7}); err != nil {
		t.Fatal(err)
	}
	r, err := a.CallExt(ctx, "plugins.apply_pack", json.RawMessage(`{"id":"daily-digest"}`))
	if err != nil {
		t.Fatal(err)
	}
	res := r.(map[string]any)
	ids := res["task_ids"].([]string)
	if len(ids) != 1 || res["button"] != true || len(res["missing_plugins"].([]string)) != 0 {
		t.Fatalf("%v", res)
	}
	v, _ := a.Tasks.Get(ctx, ids[0])
	if v.Task.CronExpr != "0 7 * * *" || !strings.Contains(v.Spec.Prompt, "https://ex.com/a.xml") || strings.Contains(v.Spec.Prompt, "not a url") ||
		len(v.Spec.AllowedTools) != 2 || !strings.Contains(strings.Join(v.Spec.AllowedTools, ","), "mcp__wecom__wecom__send_markdown") {
		t.Fatalf("%+v %+v", v.Task, v.Spec)
	}
	btn, _ := a.Auth.GetSetting(ctx, "buttons")
	if !strings.Contains(btn, ids[0]) {
		t.Fatal("button")
	}
	// Unlimited first: the preset adds no hidden time cap.
	if v.Task.TimeoutMinutes != 0 || v.Spec.TimeoutMinutes != 0 {
		t.Fatalf("preset timeout %d/%d, want 0 (no cap)", v.Task.TimeoutMinutes, v.Spec.TimeoutMinutes)
	}
	// Applying again with the same settings (a second later, files in any map
	// order) is a duplicate: no second task and no second button.
	time.Sleep(1100 * time.Millisecond)
	r2, err := a.CallExt(ctx, "plugins.apply_pack", json.RawMessage(`{"id":"daily-digest"}`))
	if err != nil {
		t.Fatal(err)
	}
	res2 := r2.(map[string]any)
	if res2["duplicate"] != true || res2["button"] == true {
		t.Fatalf("second apply %v", res2)
	}
	if ids2 := res2["task_ids"].([]string); len(ids2) != 1 || ids2[0] != ids[0] {
		t.Fatalf("second apply tasks %v, want %v", ids2, ids)
	}
	if btn2, _ := a.Auth.GetSetting(ctx, "buttons"); strings.Count(btn2, ids[0]) != 1 {
		t.Fatalf("buttons %s", btn2)
	}
	// Changed settings make a new pack and a new task.
	if err := a.Plugins.SetConfig(ctx, "daily-digest", map[string]any{"feeds": "https://ex.com/a.xml", "notify_via": "wecom", "hour": 9}); err != nil {
		t.Fatal(err)
	}
	r3, err := a.CallExt(ctx, "plugins.apply_pack", json.RawMessage(`{"id":"daily-digest"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res3 := r3.(map[string]any); res3["duplicate"] == true || res3["task_ids"].([]string)[0] == ids[0] {
		t.Fatalf("changed settings %v", res3)
	}
}

func toJSONStr(v any) string { b, _ := json.Marshal(v); return string(b) }
