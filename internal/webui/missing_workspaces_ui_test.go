package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestMissingWorkspacesUI: after boot, a pending list opens an in-app dialog
// (never a native alert) with escaped names and paths, in both languages; Got
// it dismisses; a second boot does not open another dialog.
func TestMissingWorkspacesUI(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	fn := regexp.MustCompile(`(?s)  var missingWsAsked = false;\n  function offerMissingWorkspaces\(\) \{\n.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("offerMissingWorkspaces body not found")
	}
	if regexpNative.MatchString(fn) {
		t.Error("missing-workspace notice must not use native dialogs")
	}
	for _, want := range []string{
		`api("/api/tasks/missing-workspaces")`,
		`api("/api/tasks/missing-workspaces", { method: "POST", body: {} })`,
		`T("mw.title")`,
		`T("mw.body")`,
		`T("mw.got_it")`,
		`plural(tasks.length, "mw.n_one", "mw.n")`,
		`plural(flows.length, "mw.n_wf_one", "mw.n_wf")`,
		`list(flows, "#/workflows/"`,
		`esc(t.name)`,
		`esc(t.workspace_dir)`,
		`offerMissingWorkspaces()`,
		`'<li><a href="' + base + enc(t.id)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if !strings.Contains(cssSource(t), ".mw-list {") || !strings.Contains(cssSource(t), ".mw-list .mw-path {") || !strings.Contains(cssSource(t), ".mw-list + .mw-n {") {
		t.Error("styles.css lacks .mw-list")
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
		for _, k := range []string{"mw.title", "mw.body", "mw.n", "mw.n_one", "mw.n_wf", "mw.n_wf_one", "mw.got_it"} {
			if strings.TrimSpace(m[k]) == "" {
				t.Errorf("%s missing %s", lang, k)
			}
		}
		if !strings.Contains(m["mw.n"], "{n}") || !strings.Contains(m["mw.n_one"], "{n}") || !strings.Contains(m["mw.n_wf"], "{n}") || !strings.Contains(m["mw.n_wf_one"], "{n}") {
			t.Errorf("%s: mw.n / mw.n_wf lacks {n}", lang)
		}
	}

	if !strings.Contains(src, "offerMissingWorkspaces();") {
		t.Error("boot does not call offerMissingWorkspaces")
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `var calls = [], html = "", closed = 0, Tkeys = [];
function T(k, vars) { Tkeys.push(k); return k + (vars && vars.n != null ? ":"+vars.n : ""); }
function esc(v) { return String(v == null ? "" : v).replace(/[&<>"'` + "`" + `]/g, function (c) {
  return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;", "` + "`" + `": "&#96;" }[c];
}); }
function enc(v) { return encodeURIComponent(String(v)); }
function num(n) { return n; }
function plural(n, one, many) { return T(n === 1 ? one : many, { n: n }); }
function ic() { return ""; }
function fail(e) { console.error(e); process.exitCode = 1; }
var okBtn = { disabled: false, addEventListener: function (ev, fn) { this._click = fn; } };
function $(s) { return s === "#mw-ok" ? okBtn : null; }
function $$(s) { return s === ".mw-list a" ? [{ addEventListener: function () {} }] : []; }
function api(p, o) { calls.push({ p: p, o: o || {} }); return api.ret; }
function modal(h, mount) { html = h; mount({ }, function () { closed++; }); }
` + fn + `
function bad(m) { console.error(m, JSON.stringify({ calls: calls, html: html, closed: closed, Tkeys: Tkeys })); process.exitCode = 1; }
(async function () {
  api.ret = Promise.resolve({ pending: false, tasks: [] });
  offerMissingWorkspaces();
  offerMissingWorkspaces();
  await Promise.resolve();
  if (calls.length !== 1 || calls[0].p !== "/api/tasks/missing-workspaces" || calls[0].o.method || html) bad("not pending");
  missingWsAsked = false; calls = []; html = "";
  api.ret = Promise.resolve({ pending: true, tasks: [
    { id: "t1", name: "Blog <app>", workspace_dir: "/Users/me/\"proj\"" },
    { id: "t2", name: "Notes", workspace_dir: "C:\\\\gone" }
  ] });
  offerMissingWorkspaces();
  await Promise.resolve();
  if (!html || html.indexOf("mw.title") < 0 || html.indexOf("mw.body") < 0 || html.indexOf("mw.got_it") < 0 || html.indexOf("mw.n:2") < 0) bad("dialog keys");
  if (html.indexOf("Blog <app>") >= 0 || html.indexOf("/Users/me/\"proj\"") >= 0) bad("unescaped");
  if (html.indexOf("Blog &lt;app&gt;") < 0 || html.indexOf("/Users/me/&quot;proj&quot;") < 0) bad("escaped name/path");
  if (html.indexOf("#/tasks/t1") < 0 || html.indexOf("class=\"mw-list\"") < 0) bad("links");
  if (html.indexOf("mw.n_wf") >= 0 || html.indexOf("#/workflows/") >= 0) bad("empty workflow section shown");
  if (html.indexOf("confirm(") >= 0 || html.indexOf("alert(") >= 0) bad("native");
  api.ret = Promise.resolve({ pending: false, tasks: [] });
  okBtn._click();
  await Promise.resolve();
  var post = calls.filter(function (c) { return c.o && c.o.method === "POST"; });
  if (post.length !== 1 || post[0].p !== "/api/tasks/missing-workspaces" || closed !== 1) bad("dismiss");
  missingWsAsked = false; calls = []; html = "";
  api.ret = Promise.resolve({ pending: true, tasks: [], workflows: [{ id: "wf 1", name: "Pipe <x>", workspace_dir: "D:\\old" }] });
  offerMissingWorkspaces();
  await Promise.resolve();
  if (!html || html.indexOf("mw.n_wf_one:1") < 0 || html.indexOf("mw.n:") >= 0 || html.indexOf("mw.n_one:") >= 0) bad("workflows-only sections");
  if (html.indexOf("#/workflows/wf%201") < 0 || html.indexOf("Pipe &lt;x&gt;") < 0 || html.indexOf("Pipe <x>") >= 0) bad("workflow link/escape");
  missingWsAsked = false; calls = []; html = "";
  api.ret = Promise.resolve({ pending: true, tasks: [{ id: "t1", name: "A", workspace_dir: "/a" }], workflows: [{ id: "w1", name: "B", workspace_dir: "/b" }] });
  offerMissingWorkspaces();
  await Promise.resolve();
  if (html.indexOf("mw.n_one:1") < 0 || html.indexOf("mw.n_wf_one:1") < 0 || html.indexOf("#/tasks/t1") < 0 || html.indexOf("#/workflows/w1") < 0) bad("both sections");
  if (html.indexOf("mw.n_one:1") > html.indexOf("mw.n_wf_one:1")) bad("tasks first");
})();
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("missing workspaces ui: %v\n%s", err, out)
	}
}
