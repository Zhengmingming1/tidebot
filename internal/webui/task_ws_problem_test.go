package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// TestTaskListWorkspaceProblem: the task list marks a task whose own folder the
// next run would refuse (guard.CheckPlannedDir, as runTaskTriggerOn) or, for a
// task that has run before, whose folder is gone (the next run would start in a
// new empty one). Default folders, never-run planned paths and healthy folders
// carry nothing, and listing creates nothing.
func TestTaskListWorkspaceProblem(t *testing.T) {
	c := login(t)
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	keys := filepath.Join(userHome, ".ssh")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	n := 0
	mk := func(name, ws string, ran bool) string {
		t.Helper()
		code, out := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": name, "prompt": "p", "provider": "anthropic", "workspace": ws})
		if code >= 300 {
			t.Fatalf("create %s: %d %v", name, code, out)
		}
		id := fmt.Sprint(out["id"])
		if ran {
			n++
			rid := fmt.Sprintf("r%02d", n)
			seedRun(t, c, rid, "p", "succeeded", 0.01, time.Now())
			if _, err := c.s.deps.DB.Exec(`UPDATE runs SET task_id=? WHERE id=?`, id, rid); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	okDir, goneDir, fileDir, linkDir := dir("ok"), dir("gone"), dir("file"), dir("link")
	planned := filepath.Join(root, "later", "site")
	want := map[string]string{
		mk("Default", "", true):         "",
		mk("Fine", okDir, true):         "",
		mk("Planned", planned, false):   "",
		mk("Gone", goneDir, true):       taskWsMissing,
		mk("Now a file", fileDir, true): guard.DirNotDir,
	}
	linkID := mk("Now a link", linkDir, false)
	// After saving: the folders move away, turn into a file or a link into ~/.ssh.
	for _, p := range []string{goneDir, fileDir, linkDir} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(fileDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keys, linkDir); err == nil {
		want[linkID] = guard.DirSensitive
	} else {
		want[linkID] = "" // no symlinks here (Windows without the privilege): never run, so a missing path is fine
	}

	rr := c.do(http.MethodGet, "/api/tasks", "", nil)
	if rr.Code != 200 {
		t.Fatalf("list %d %s", rr.Code, rr.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != len(want) {
		t.Fatalf("listed %d, want %d", len(list), len(want))
	}
	for _, tk := range list {
		id := fmt.Sprint(tk["id"])
		got, has := tk["workspace_problem"].(string)
		if want[id] == "" && has {
			t.Errorf("%s: unexpected workspace_problem %q", tk["name"], got)
		}
		if got != want[id] {
			t.Errorf("%s: workspace_problem %q, want %q", tk["name"], got, want[id])
		}
	}
	for _, p := range []string{goneDir, planned} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was created by the list: %v", p, err)
		}
	}
}

// TestTaskRowWorkspacePill: the row shows an escaped in-app pill next to the
// name only when there is a problem: red for a refused run, amber for a folder
// that would be recreated empty; unknown codes fall back to "not found".
func TestTaskRowWorkspacePill(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	fn := regexp.MustCompile(`(?s)  function taskWsPill\(t\) \{\n.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("taskWsPill not found")
	}
	if !strings.Contains(src, `costModeBadge(t.cost_control_mode) + taskWsPill(t) + tagChipsRow(t.tags)`) {
		t.Error("task row does not render taskWsPill")
	}
	if regexpNative.MatchString(fn) {
		t.Error("no native dialogs")
	}
	for _, lang := range []string{"zh-CN", "en"} {
		raw, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(m["tl.ws_bad_tip"], "{why}") || !strings.Contains(m["tl.ws_bad_tip"], "{p}") {
			t.Errorf("%s: tl.ws_bad_tip lacks {why}/{p}", lang)
		}
		if !strings.Contains(m["tl.ws_missing_tip"], "{p}") {
			t.Errorf("%s: tl.ws_missing_tip lacks {p}", lang)
		}
		for _, p := range []string{guard.DirNotFound, guard.DirNotDir, guard.DirSensitive, guard.DirRelative, guard.DirContainsSensitive, guard.DirInternal, guard.DirRoot} {
			if m["rn.ws_"+p] == "" || m["rn.ws_"+p+"_tip"] == "" {
				t.Errorf("%s: no text for problem %s", lang, p)
			}
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `var state = { dict: { "rn.ws_not_found": "x", "rn.ws_not_found_tip": "x", "rn.ws_sensitive": "x", "rn.ws_sensitive_tip": "x" } };
function T(k, v) { return k + (v ? "[" + Object.keys(v).map(function (n) { return n + "=" + v[n]; }).join("|") + "]" : ""); }
function esc(v) { return String(v == null ? "" : v).replace(/[&<>"']/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]; }); }
` + fn + `
function bad(m, h) { console.error(m, h); process.exitCode = 1; }
var h = taskWsPill({ id: "t", workspace: "/a" });
if (h !== "") bad("pill without problem", h);
h = taskWsPill({ id: "t", workspace_problem: "missing", workspace: "/Users/me/<\"x\">" });
if (h.indexOf("pill warn sm tl-ws-pill") < 0 || h.indexOf("tl.ws_missing_tip[p=") < 0 || h.indexOf("s=rn.ws_not_found]") < 0) bad("missing", h);
if (h.indexOf("<\"x\">") >= 0 || h.indexOf("/Users/me/&lt;&quot;x&quot;&gt;") < 0) bad("escape", h);
h = taskWsPill({ id: "t", workspace_problem: "sensitive", workspace: "/k" });
if (h.indexOf("pill bad sm tl-ws-pill") < 0 || h.indexOf("s=rn.ws_sensitive]") < 0 || h.indexOf("why=rn.ws_sensitive_tip") < 0) bad("sensitive", h);
h = taskWsPill({ id: "t", workspace_problem: "brand_new", workspace: "C:\\old" });
if (h.indexOf("s=rn.ws_not_found]") < 0 || h.indexOf("brand_new") >= 0) bad("fallback", h);
`
	cmd := exec.Command(node, "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

// TestTaskDetailWorkspaceProblem: the task detail answer carries the same
// problem as its list row (checked on the saved folder, before an empty one
// becomes the default), and a workflow task carries the problem of the folder
// in the workflow version it runs: pinned to an old version with a gone folder
// it is flagged, following the current one with an existing folder it is not.
func TestTaskDetailWorkspaceProblem(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	svc := workflow.New(c.s.deps.DB, func(context.Context, workflow.Node, string, string, func(string)) (string, error) {
		return "", nil
	})
	c.s.deps.Workflows = svc
	root := t.TempDir()
	gone, kept, planned := filepath.Join(root, "gone"), filepath.Join(root, "kept"), filepath.Join(root, "later")
	for _, p := range []string{gone, kept} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(body map[string]any) string {
		t.Helper()
		body["provider"] = "anthropic"
		code, out := c.json(http.MethodPost, "/api/tasks", body)
		if code >= 300 {
			t.Fatalf("create %v: %d %v", body["name"], code, out)
		}
		return fmt.Sprint(out["id"])
	}
	ranID := mk(map[string]any{"name": "Ran", "prompt": "p", "workspace": gone})
	seedRun(t, c, "r-det", "p", "succeeded", 0.01, time.Now())
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET task_id=? WHERE id=?`, ranID, "r-det"); err != nil {
		t.Fatal(err)
	}
	neverID := mk(map[string]any{"name": "Never ran", "prompt": "p", "workspace": planned})
	defID := mk(map[string]any{"name": "Default", "prompt": "p"})

	bind := []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}
	def := func(ws string) workflow.Definition {
		return workflow.Definition{Name: "Flow", WorkspaceDir: ws, Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: bind}}}
	}
	w1, err := svc.Save(ctx, "", 0, def(gone))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, w1.ID, w1.Version, def(kept)); err != nil {
		t.Fatal(err)
	}
	pinID := mk(map[string]any{"name": "Pinned", "workflow": map[string]any{"id": w1.ID, "version": w1.Version}})
	followID := mk(map[string]any{"name": "Follows", "workflow": map[string]any{"id": w1.ID, "version": 0}})
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	type exp struct{ prob, wfDir string }
	want := map[string]exp{
		ranID:    {taskWsMissing, ""},
		neverID:  {"", ""},
		defID:    {"", ""},
		pinID:    {guard.DirNotFound, gone},
		followID: {"", ""},
	}
	rr := c.do(http.MethodGet, "/api/tasks", "", nil)
	var list []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != len(want) {
		t.Fatalf("listed %d, want %d", len(list), len(want))
	}
	for _, tk := range list {
		w := want[fmt.Sprint(tk["id"])]
		if got, _ := tk["workspace_problem"].(string); got != w.prob {
			t.Errorf("list %s: workspace_problem %q, want %q", tk["name"], got, w.prob)
		}
		if got, _ := tk["workflow_workspace"].(string); got != w.wfDir {
			t.Errorf("list %s: workflow_workspace %q, want %q", tk["name"], got, w.wfDir)
		}
	}
	for id, w := range want {
		code, d := c.json(http.MethodGet, "/api/tasks/"+id, nil)
		if code != 200 {
			t.Fatalf("get %s: %d", id, code)
		}
		if got, _ := d["workspace_problem"].(string); got != w.prob {
			t.Errorf("detail %s: workspace_problem %q, want %q", d["name"], got, w.prob)
		}
		if got, _ := d["workflow_workspace"].(string); got != w.wfDir {
			t.Errorf("detail %s: workflow_workspace %q, want %q", d["name"], got, w.wfDir)
		}
	}
	if code, d := c.json(http.MethodGet, "/api/tasks/"+defID, nil); code != 200 || d["workspace_default"] != true {
		t.Fatalf("default folder still resolved in the detail answer: %v", d)
	}
	// Read-only: neither the list nor the detail created the missing folders.
	for _, p := range []string{gone, planned} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was created: %v", p, err)
		}
	}
}

// TestTaskDetailWorkspacePill: the overview shows the pill next to the folder
// row (for a workflow task that row is the workflow's folder), whose tooltip
// names the workflow's folder for a workflow task.
func TestTaskDetailWorkspacePill(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	// one pill, next to the folder the runs use (a workflow task's row shows
	// the workflow's folder), not next to the workflow version tag
	if !strings.Contains(src, `T("ws.wf"))+"</span>":"")+taskWsPill(tk)`) && !strings.Contains(src, `T("ws.wf")) + "</span>" : "") + taskWsPill(tk)`) {
		t.Error("overview folder row does not render taskWsPill")
	}
	if strings.Contains(src, `T("tf.wf_follow"))+"</span>"+taskWsPill(tk)`) || strings.Contains(src, `T("tf.wf_follow")) + "</span>" + taskWsPill(tk)`) {
		t.Error("overview workflow row still renders taskWsPill")
	}
	fn := regexp.MustCompile(`(?s)  function taskWsPill\(t\) \{\n.*?\n  \}\n`).FindString(src)
	node, err := exec.LookPath("node")
	if fn == "" || err != nil {
		t.Skip("node not installed or taskWsPill not found")
	}
	script := `var state = { dict: { "rn.ws_not_found": "x", "rn.ws_not_found_tip": "x" } };
function T(k, v) { return k + (v ? "[" + Object.keys(v).map(function (n) { return n + "=" + v[n]; }).join("|") + "]" : ""); }
function esc(v) { return String(v == null ? "" : v).replace(/[&<>"']/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]; }); }
` + fn + `
function bad(m, h) { console.error(m, h); process.exitCode = 1; }
var h = taskWsPill({ id: "t", workflow: { id: "w" }, workspace: "/own", workspace_problem: "not_found", workflow_workspace: "/old/<b>" });
if (h.indexOf("pill bad sm tl-ws-pill") < 0 || h.indexOf("wf.ws_bad_tip[why=rn.ws_not_found_tip|p=/old/&lt;b&gt;]") < 0 || h.indexOf("/own") >= 0) bad("workflow task", h);
if (taskWsPill({ id: "t", workflow: { id: "w" } }) !== "") bad("workflow task without problem", "");
h = taskWsPill({ id: "t", workflow: { id: "w" }, workspace_problem: "missing", workflow_workspace: "/x" });
if (h.indexOf("pill bad") < 0 || h.indexOf("s=rn.ws_not_found]") < 0) bad("workflow fallback", h);
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
