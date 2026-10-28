package webui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// The task form sets a working folder: typed or picked, cleaned, checked before save,
// and cleared back to the per-task default.
func TestTaskFormWorkspace(t *testing.T) {
	c := login(t)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	c.s.deps.Cfg.DataDir = filepath.Join(c.s.deps.Cfg.HomeDir, "data")
	proj := filepath.Join(t.TempDir(), "proj")

	// Create with a folder of its own (quoted, with a trailing separator, as pasted).
	code, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "周报", "prompt": "p", "provider": "anthropic",
		"workspace": ` "` + proj + string(filepath.Separator) + `" `})
	id, _ := tk["id"].(string)
	if code != 201 || id == "" {
		t.Fatalf("create %d %v", code, tk)
	}
	_, d := c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["workspace"] != proj || d["workspace_default"] != nil {
		t.Fatalf("own folder %v %v", d["workspace"], d["workspace_default"])
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if f, _ := form["form"].(map[string]any); f["workspace"] != proj {
		t.Fatalf("form workspace %v", form)
	}

	edit := func(ws any) (int, map[string]any) {
		body := map[string]any{"name": "周报", "prompt": "p", "provider": "anthropic", "enabled": true, "timeout_minutes": 30}
		if ws != nil {
			body["workspace"] = ws
		}
		return c.json(http.MethodPut, "/api/tasks/"+id, body)
	}
	// An older page that doesn't send the field keeps the folder.
	if code, _ := edit(nil); code != 200 {
		t.Fatalf("edit without field %d", code)
	}
	if _, d = c.json(http.MethodGet, "/api/tasks/"+id, nil); d["workspace"] != proj {
		t.Fatalf("folder lost by an edit without the field: %v", d["workspace"])
	}
	// Bad folders are refused and leave the task as it was.
	file := filepath.Join(t.TempDir(), "notes.txt")
	_ = os.WriteFile(file, []byte("x"), 0o600)
	root := filepath.VolumeName(proj) + string(filepath.Separator)
	for _, bad := range []string{"relative/dir", root, c.s.deps.Cfg.HomeDir, filepath.Join(c.s.deps.Cfg.HomeDir, "secrets"), file, strings.Repeat("a", 2000)} {
		if code, out := edit(bad); code != 400 || !strings.Contains(out["error"].(string), "working folder") {
			t.Errorf("%q accepted: %d %v", bad, code, out)
		}
	}
	if _, d = c.json(http.MethodGet, "/api/tasks/"+id, nil); d["workspace"] != proj {
		t.Fatalf("bad edit changed the folder: %v", d["workspace"])
	}
	// Empty → back to the per-task default; the row is cleared too, so runs use it.
	def := guard.TaskWorkspace(c.s.deps.Cfg.HomeDir, id)
	if code, _ := edit(""); code != 200 {
		t.Fatalf("clear %d", code)
	}
	if _, d = c.json(http.MethodGet, "/api/tasks/"+id, nil); d["workspace"] != def || d["workspace_default"] != true {
		t.Fatalf("cleared %v %v", d["workspace"], d["workspace_default"])
	}
	// Naming the default folder or the data folder also means "default".
	for _, same := range []string{def, c.s.deps.Cfg.DataDir} {
		if code, _ := edit(same); code != 200 {
			t.Fatalf("edit %q", same)
		}
		_, form = c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
		if f, _ := form["form"].(map[string]any); f["workspace"] != "" {
			t.Fatalf("%q stored as own folder: %v", same, f["workspace"])
		}
	}
	// "~" expands to the user's home.
	if home, err := os.UserHomeDir(); err == nil && !within(home, c.s.deps.Cfg.HomeDir) {
		if code, _ := edit("~/tb-proj"); code != 200 {
			t.Fatal("tilde path refused")
		}
		if _, d = c.json(http.MethodGet, "/api/tasks/"+id, nil); d["workspace"] != filepath.Join(home, "tb-proj") {
			t.Fatalf("tilde %v", d["workspace"])
		}
	}
}

func TestWorkspaceCheck(t *testing.T) {
	c := login(t)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	check := func(q string) map[string]any {
		code, out := c.json(http.MethodGet, "/api/tasks/workspace-check?"+q, nil)
		if code != 200 {
			t.Fatalf("check %s: %d %v", q, code, out)
		}
		return out
	}
	sep := string(filepath.Separator)
	if out := check(""); out["default"] != filepath.Join(c.s.deps.Cfg.HomeDir, "workspaces")+sep+"…" || out["problem"] != "" {
		t.Fatalf("new-task default %v", out)
	}
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "a", "prompt": "p", "provider": "anthropic"})
	id := tk["id"].(string)
	if out := check("id=" + url.QueryEscape(id)); out["default"] != guard.TaskWorkspace(c.s.deps.Cfg.HomeDir, id) {
		t.Fatalf("task default %v", out)
	}
	dir := t.TempDir()
	if out := check("path=" + url.QueryEscape(dir)); out["exists"] != true || out["path"] != dir {
		t.Fatalf("existing %v", out)
	}
	if out := check("path=" + url.QueryEscape(filepath.Join(dir, "new"))); out["exists"] != false || out["problem"] != "" {
		t.Fatalf("new %v", out)
	}
	for q, want := range map[string]string{"rel": "relative", filepath.Join(c.s.deps.Cfg.HomeDir, "x"): "internal"} {
		if out := check("path=" + url.QueryEscape(q)); out["problem"] != want {
			t.Errorf("%s: %v", q, out)
		}
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/workspace-check", nil); code != 405 {
		t.Errorf("post %d", code)
	}
}

func TestPickFolder(t *testing.T) {
	c := login(t)
	if code, out := c.json(http.MethodGet, "/api/desktop/pick-folder", nil); code != 200 || out["available"] != false {
		t.Fatalf("browser get %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/pick-folder", map[string]string{}); code != 501 {
		t.Fatalf("browser post %d", code)
	}
	dir := t.TempDir()
	var starts []string
	c.s.PickFolder = func(start string) (string, error) { starts = append(starts, start); return dir, nil }
	if code, out := c.json(http.MethodPost, "/api/desktop/pick-folder", map[string]string{"start": dir}); code != 200 || out["path"] != dir {
		t.Fatalf("pick %d %v", code, out)
	}
	// A start that isn't an existing folder opens the dialog at the OS default.
	_, _ = c.json(http.MethodPost, "/api/desktop/pick-folder", map[string]string{"start": filepath.Join(dir, "missing")})
	_, _ = c.json(http.MethodPost, "/api/desktop/pick-folder", map[string]string{"start": "rel"})
	if len(starts) != 3 || starts[0] != dir || starts[1] != "" || starts[2] != "" {
		t.Fatalf("starts %q", starts)
	}
	c.s.PickFolder = func(string) (string, error) { return "", nil }
	if _, out := c.json(http.MethodPost, "/api/desktop/pick-folder", map[string]string{}); out["canceled"] != true {
		t.Fatalf("cancel %v", out)
	}
}

func TestTaskFormWorkspaceUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`wsFieldHtml(tf.workspace || "")`,
		`var ws = wireWsField(root, f.workspace, taskId);`,
		`workspace: f.workspace.value.trim(), policy: readPolicyFields(f), tags: tgs.value() }`,
		`if (ws.problem()) {`,
		`"/api/tasks/workspace-check?path="`,
		`api("/api/desktop/pick-folder", { method: "POST", body: { start: start } })`,
		`input._tbTouched = true;`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".ws-field > span .pill {") {
		t.Error("styles.css lacks .ws-field pill")
	}
	keys := []string{"tf.workspace", "tf.ws_hint", "tf.ws_default_ph", "tf.ws_pick", "tf.ws_reset", "tf.ws_new", "tf.ws_new_tip", "tf.ws_bad"}
	for _, p := range []string{"relative", "root", "internal", "not_dir", "too_long"} {
		keys = append(keys, "tf.ws_err_"+p)
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		if !strings.Contains(m["tf.ws_default_ph"], "{p}") {
			t.Errorf("%s: tf.ws_default_ph lacks {p}", f)
		}
	}
}
