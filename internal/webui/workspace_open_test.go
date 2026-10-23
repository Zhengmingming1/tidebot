package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// The task page shows the folder runs really work in (tidebot's per-task default when the
// task has none), and the desktop app opens it by task id only.
func TestTaskWorkspaceShownAndOpened(t *testing.T) {
	c := login(t)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "日报", "prompt": "p", "provider": "anthropic"})
	id, _ := tk["id"].(string)
	if id == "" {
		t.Fatalf("create %v", tk)
	}
	def := guard.TaskWorkspace(c.s.deps.Cfg.HomeDir, id)
	_, d := c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["workspace"] != def || d["workspace_default"] != true {
		t.Fatalf("default workspace %v %v", d["workspace"], d["workspace_default"])
	}
	// The list answer stays as it was (no per-row path work).
	rr := c.do(http.MethodGet, "/api/tasks", "", nil)
	if strings.Contains(rr.Body.String(), "workspace_default") {
		t.Fatal("list should not carry workspace_default")
	}

	// GET names the OS so the button reads Finder / File Explorer / file manager.
	if code, out := c.json(http.MethodGet, "/api/desktop/open", nil); code != 200 || out["os"] != runtime.GOOS {
		t.Fatalf("get %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "workspace", "id": id}); code != 501 {
		t.Fatalf("browser post %d", code)
	}
	var opened []string
	c.s.OpenPath = func(p string, reveal bool) error {
		if reveal {
			t.Errorf("workspace opened with reveal: %s", p)
		}
		opened = append(opened, p)
		return nil
	}
	if _, err := os.Stat(def); err == nil {
		t.Fatal("default folder exists before first open")
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "workspace", "id": id}); code != 200 || len(opened) != 1 || opened[0] != def {
		t.Fatalf("open default %d %v", code, opened)
	}
	if st, err := os.Stat(def); err != nil || !st.IsDir() {
		t.Fatalf("default folder not created: %v", err)
	}

	// A folder of its own is shown and opened as is, and never created by the UI.
	own := filepath.Join(t.TempDir(), "proj")
	if _, err := c.s.deps.DB.ExecContext(context.Background(), `UPDATE tasks SET workspace_dir=? WHERE id=?`, own, id); err != nil {
		t.Fatal(err)
	}
	_, d = c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if d["workspace"] != own || d["workspace_default"] != nil {
		t.Fatalf("own workspace %v %v", d["workspace"], d["workspace_default"])
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "workspace", "id": id}); code != 200 || opened[1] != own {
		t.Fatalf("open own %d %v", code, opened)
	}
	if _, err := os.Stat(own); err == nil {
		t.Fatal("UI created a task's own folder")
	}
	// The data folder as workspace means "default", like the runner treats it.
	_, _ = c.s.deps.DB.ExecContext(context.Background(), `UPDATE tasks SET workspace_dir=? WHERE id=?`, c.s.deps.Cfg.DataDir, id)
	if _, d = c.json(http.MethodGet, "/api/tasks/"+id, nil); d["workspace"] != def {
		t.Fatalf("data dir workspace %v", d["workspace"])
	}

	// Unknown / missing ids and path-looking ids never reach the file manager.
	n := len(opened)
	for _, body := range []map[string]string{{"target": "workspace"}, {"target": "workspace", "id": "nope"}, {"target": "workspace", "id": "../" + id}, {"target": "workspace", "id": def}} {
		if code, _ := c.json(http.MethodPost, "/api/desktop/open", body); code != 404 {
			t.Fatalf("%v → %d", body, code)
		}
	}
	if len(opened) != n {
		t.Fatalf("file manager called for bad input: %v", opened[n:])
	}
}

func TestTaskWorkspaceUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`body: { target: "workspace", id: t.id }`,
		`copyText(t.workspace)`,
		`desktopOS === "darwin" ? "ws.open_mac" : desktopOS === "windows" ? "ws.open_win" : "ws.open_linux"`,
		`o: ["#ws-open"]`,
		`if (hit && hit.classList.contains("hidden")) hit = null;`,
		`tk.workspace_default ?`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".kv dd.ws-row {") {
		t.Error("styles.css lacks .ws-row")
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range []string{"ws.copy", "ws.default", "ws.default_tip", "ws.open_mac", "ws.open_win", "ws.open_linux", "ws.missing"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		if !strings.Contains(m["sc.task_keys"], "o ") {
			t.Errorf("%s: shortcut help lacks o", f)
		}
	}
}
