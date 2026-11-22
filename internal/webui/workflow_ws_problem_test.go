package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

// TestWorkflowListWorkspaceProblem: the workflow library and a single workflow
// carry the same folder problem a run would hit (guard.CheckRunDir after "~"
// expansion), so a card can say "Folder: not found" before Run is clicked.
// Workflows without a folder or with an existing one carry nothing; checking
// never creates the folder or rewrites the saved definition.
func TestWorkflowListWorkspaceProblem(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(userHome, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(userHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	here := t.TempDir()
	gone := filepath.Join(t.TempDir(), "old-machine", "site")
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bind := []tasksvc.SkillBinding{{SkillID: "fixture", Mode: "follow"}}
	save := func(name, ws string) string {
		t.Helper()
		w, err := svc.Save(ctx, "", 0, workflow.Definition{Name: name, WorkspaceDir: ws,
			Nodes: []workflow.Node{{ID: "a", Kind: "skill", Prompt: "{{input}}", SkillBindings: bind}}})
		if err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
		return w.ID
	}
	want := map[string]string{
		save("No folder", ""):                         "",
		save("Here", here):                            "",
		save("Tilde", "~/proj"):                       "",
		save("Gone", gone):                            guard.DirNotFound,
		save("Tilde gone", "~/nope"):                  guard.DirNotFound,
		save("A file", file):                          guard.DirNotDir,
		save("Keys", filepath.Join(userHome, ".ssh")): guard.DirSensitive,
	}

	rr := c.do(http.MethodGet, "/api/workflows", "", nil)
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
	for _, w := range list {
		id := w["id"].(string)
		got, has := w["workspace_problem"].(string)
		if want[id] == "" && has {
			t.Errorf("%s: unexpected workspace_problem %q", w["name"], got)
		}
		if got != want[id] {
			t.Errorf("%s: workspace_problem %q, want %q", w["name"], got, want[id])
		}
	}
	for id, p := range want {
		code, one := c.json(http.MethodGet, "/api/workflows/"+id, nil)
		if code != 200 {
			t.Fatalf("get %d", code)
		}
		if got, _ := one["workspace_problem"].(string); got != p {
			t.Errorf("GET %s: %q, want %q", one["name"], got, p)
		}
	}
	// Checking is read-only: the missing folders are still missing.
	for _, p := range []string{gone, filepath.Join(userHome, "nope")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was created by the list: %v", p, err)
		}
	}
}

// TestWorkflowCardWorkspacePill: a card shows an escaped in-app pill (tooltip
// with reason and path) linking to the editor, only when there is a problem;
// unknown problem codes fall back to "not found" text.
func TestWorkflowCardWorkspacePill(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	fn := regexp.MustCompile(`(?s)  function wfWsPill\(w\) \{\n.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("wfWsPill not found")
	}
	if !strings.Contains(src, `<span class="grow"></span>'+wfWsPill(w)+wfTasksPill(w)+`) {
		t.Error("library card does not render wfWsPill")
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
		if !strings.Contains(m["wf.ws_bad"], "{s}") {
			t.Errorf("%s: wf.ws_bad lacks {s}", lang)
		}
		if !strings.Contains(m["wf.ws_bad_tip"], "{why}") || !strings.Contains(m["wf.ws_bad_tip"], "{p}") {
			t.Errorf("%s: wf.ws_bad_tip lacks {why}/{p}", lang)
		}
		for _, p := range []string{guard.DirNotFound, guard.DirNotDir, guard.DirSensitive, guard.DirRelative} {
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
function enc(v) { return encodeURIComponent(String(v)); }
function ic() { return ""; }
` + fn + `
function bad(m, h) { console.error(m, h); process.exitCode = 1; }
var h = wfWsPill({ id: "w 1", definition: { workspace_dir: "/a" } });
if (h !== "") bad("pill without problem", h);
h = wfWsPill({ id: "w 1", workspace_problem: "not_found", definition: { workspace_dir: "/Users/me/<\"x\">" } });
if (h.indexOf('href="#/workflows/w%201"') < 0 || h.indexOf("pill bad sm") < 0) bad("link/class", h);
if (h.indexOf("wf.ws_bad[s=rn.ws_not_found]") < 0 || h.indexOf("why=rn.ws_not_found_tip") < 0) bad("keys", h);
if (h.indexOf("<\"x\">") >= 0 || h.indexOf("/Users/me/&lt;&quot;x&quot;&gt;") < 0) bad("escape", h);
h = wfWsPill({ id: "w", workspace_problem: "sensitive", definition: { workspace_dir: "/k" } });
if (h.indexOf("s=rn.ws_sensitive]") < 0) bad("sensitive", h);
h = wfWsPill({ id: "w", workspace_problem: "brand_new", definition: { workspace_dir: "C:\\old" } });
if (h.indexOf("s=rn.ws_not_found]") < 0 || h.indexOf("brand_new") >= 0) bad("fallback", h);
`
	cmd := exec.Command(node, "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
