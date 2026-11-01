package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestUndoFileAPI: the file path reaches the app, the answer carries the files taken
// back one by one, the detail lists them and the runs list counts them; an oversize
// path is refused before the app sees it.
func TestUndoFileAPI(t *testing.T) {
	c := login(t)
	seedRun(t, c, "uf1", "p", "succeeded", 0, time.Now())
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET run_mode='edit', workspace_dir='/w', file_changes='{"files":[{"path":"a.txt"},{"path":"b.txt"}],"count":2,"add":3,"del":1}' WHERE id='uf1'`); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	called := false
	c.s.deps.UndoRunChanges = func(ctx context.Context, id string, redo bool, path string) (UndoResult, error) {
		called, gotPath = true, path
		_, err := c.s.deps.DB.Exec(`UPDATE runs SET changes_undone_files='["a.txt"]' WHERE id=?`, id)
		return UndoResult{UndoneFiles: []string{"a.txt"}}, err
	}
	code, out := c.json(http.MethodPost, "/api/runs/uf1/undo-changes", map[string]any{"path": "a.txt"})
	if code != 200 || gotPath != "a.txt" || out["changes_undone_at"] != "" {
		t.Fatalf("undo file %d %v %q", code, out, gotPath)
	}
	if fs, _ := out["changes_undone_files"].([]any); len(fs) != 1 || fs[0] != "a.txt" {
		t.Fatalf("answer files %v", out)
	}
	if code, out = c.json(http.MethodGet, "/api/runs/uf1", nil); code != 200 {
		t.Fatalf("detail %d", code)
	}
	if fs, _ := out["changes_undone_files"].([]any); len(fs) != 1 || fs[0] != "a.txt" {
		t.Fatalf("detail files %v", out["changes_undone_files"])
	}
	if rec := c.do(http.MethodGet, "/api/runs?limit=5", "", nil); !strings.Contains(rec.Body.String(), `"undone_n":1`) {
		t.Fatalf("list lacks undone_n: %s", rec.Body.String())
	}
	// wholly undone: the per-file list isn't reported
	_, _ = c.s.deps.DB.Exec(`UPDATE runs SET changes_undone_at='2026-11-01T00:00:00Z' WHERE id='uf1'`)
	if _, out = c.json(http.MethodGet, "/api/runs/uf1", nil); out["changes_undone_files"] != nil {
		t.Fatalf("whole run lists files %v", out["changes_undone_files"])
	}
	called = false
	if code, _ = c.json(http.MethodPost, "/api/runs/uf1/undo-changes", map[string]any{"path": strings.Repeat("x", 5000)}); code != 400 || called {
		t.Fatalf("long path %d called=%v", code, called)
	}
	if code, _ = c.json(http.MethodPost, "/api/runs/uf1/undo-changes", map[string]any{"path": "a\x00b"}); code != 400 || called {
		t.Fatalf("NUL path %d called=%v", code, called)
	}
}

// TestUndoFileUI: each file row has its own undo (or put back when undone), hidden
// while running, for a cut patch or when not every file is listed; a partly undone run
// says "n/m undone" and offers "Undo the rest"; texts in both languages.
func TestUndoFileUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`api("/api/runs/" + enc(r.id) + "/undo-changes", { method: "POST", body: { redo: redo, path: path } })`,
		`return confirmDialog({ title: T("rd.undo_f_title", { f: path })`,
		`e.preventDefault(); e.stopPropagation();`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	if i := strings.Index(src, "function runUndoFile"); i < 0 || regexp.MustCompile(`\b(confirm|alert|prompt)\(`).MatchString(src[i:i+1200]) {
		t.Error("runUndoFile missing or uses a native dialog")
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".rd-chg-fx", ".diff-file.gone", "@media (hover: none) { .rd-chg-fx", ".chg-badge.partly"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles lack %s", s)
		}
	}
	keys := map[string]string{"rd.chg_undo_rest": "", "rd.chg_undo_rest_tip": "", "rd.chg_partly": "{m}", "rd.chg_partly_tip": "", "rd.chg_f_undo": "", "rd.chg_f_redo": "",
		"rd.chg_f_undone": "{f}", "rd.chg_f_redone": "{f}", "rd.undo_f_title": "{f}", "rd.undo_f_body": "{dir}", "rd.undo_err_file": "", "rd.undo_err_many": "", "runs.chg_partly_tip": "{n}"}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for k, ph := range keys {
			if m[k] == "" || !strings.Contains(m[k], ph) {
				t.Errorf("%s %s missing or lacks %q", f, k, ph)
			}
		}
	}
	fn := regexp.MustCompile(`(?s)  function runChangesHtml\(r\) \{\n.*?\n  function bindRunChanges`).FindString(src)
	badge := regexp.MustCompile(`(?s)  function runChgBadge\(r\) \{\n.*?\n  \}\n`).FindString(src)
	if fn == "" || badge == "" {
		t.Fatal("functions not found")
	}
	fn = strings.TrimSuffix(fn, "  function bindRunChanges")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");}
var state = { dict: {} };
function T(k,v){return k+(v?JSON.stringify(v):"");} function ic(n){return "<i>"+n+"</i>";} function num(n){return String(n);}
function when(t){return "when:"+t;} function enc(s){return encodeURIComponent(s);} function plural(n,a,b){return T(n===1?a:b,{n:n});}
` + fn + badge + `
var fail = function (m, h) { console.error(m + "\n" + h); process.exitCode = 1; };
var files = [{ path: "a.txt", status: "M", add: 1, del: 1 }, { path: "<b>.txt", status: "A", add: 2, del: 0 }];
var c = { files: files, count: 2, add: 3, del: 1 };
var h = runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: c });
if ((h.match(/data-chg-file="undo"/g) || []).length !== 2 || h.indexOf("rd.chg_partly") >= 0 || h.indexOf("<b>.txt") >= 0) fail("rows", h);
h = runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: c, changes_undone_files: ["<b>.txt"] });
if ((h.match(/data-chg-file="redo"/g) || []).length !== 1 || h.indexOf('class="diff-file gone" data-chg="1"') < 0 || h.indexOf('rd.chg_partly{&quot;n&quot;:&quot;1&quot;,&quot;m&quot;:&quot;2&quot;}') < 0 ||
    h.indexOf("rd.chg_undo_rest<") < 0 || h.indexOf('data-chg-undo="undo"') < 0) fail("partly", h);
h = runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: c, changes_undone_at: "2026-11-01T00:00:00Z", changes_undone_files: ["a.txt"] });
if ((h.match(/data-chg-file="redo"/g) || []).length !== 2 || h.indexOf("rd.chg_partly") >= 0 || h.indexOf(" gone") >= 0) fail("whole", h);
["running", "queued"].forEach(function (s) {
  if (runChangesHtml({ id: "r1", run_mode: "edit", status: s, file_changes: c }).indexOf("data-chg-file") >= 0) fail("live " + s, "");
});
if (runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: { files: files, count: 2, patch_cut: true } }).indexOf("data-chg-file") >= 0) fail("cut", "");
if (runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: { files: files, count: 400 } }).indexOf("data-chg-file") >= 0) fail("over cap", "");
var b = runChgBadge({ id: "r1", changes: { files: 2, add: 3, del: 0, undone_n: 1 } });
if (b.indexOf('class="chg-badge partly"') < 0 || b.indexOf('runs.chg_partly_tip{&quot;n&quot;:&quot;1&quot;}') < 0) fail("badge partly", b);
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
