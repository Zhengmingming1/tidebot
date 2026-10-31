package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

const samplePatch = "diff --git a/a.txt b/a.txt\nindex 1..2 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1,3 +1,3 @@ func x\n one\n-two\n+TWO\n three\n" +
	"diff --git a/bin.dat b/bin.dat\nnew file mode 100644\nindex 0..3\nBinary files /dev/null and b/bin.dat differ\n" +
	"diff --git a/n.txt b/n.txt\nnew file mode 100644\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1 @@\n+x\n\\ No newline at end of file\n" +
	"diff --git a/run.sh b/run.sh\nold mode 100644\nnew mode 100755\n"

func TestParsePatch(t *testing.T) {
	d := ParsePatch(samplePatch)
	if len(d) != 4 {
		t.Fatalf("%d files", len(d))
	}
	l := d[0].Lines
	if len(l) != 5 || l[0].Op != "@" || l[1].A != 1 || l[1].B != 1 || l[2].Op != "-" || l[2].A != 2 || l[3].Op != "+" || l[3].B != 2 || l[3].Text != "TWO" || l[4].A != 3 || l[4].B != 3 {
		t.Fatalf("a.txt %+v", l)
	}
	if !d[1].Binary || len(d[1].Lines) != 0 {
		t.Fatalf("bin %+v", d[1])
	}
	if l := d[2].Lines; len(l) != 3 || l[1].B != 1 || l[2].Op != "\\" || l[2].Text != "No newline at end of file" {
		t.Fatalf("n.txt %+v", l)
	}
	if len(d[3].Lines) != 0 || d[3].Binary {
		t.Fatalf("mode only %+v", d[3])
	}
	// CRLF patch reads the same; header lines like "--- a/x" outside a hunk aren't lines
	if c := ParsePatch(strings.ReplaceAll(samplePatch, "\n", "\r\n")); len(c) != 4 || c[0].Lines[3].Text != "TWO" {
		t.Fatalf("crlf %+v", c)
	}
	if len(ParsePatch("")) != 0 {
		t.Fatal("empty")
	}
	// long lines are clipped, many lines cut
	big := "diff --git a/b b/b\n@@ -0,0 +1,5000 @@\n" + strings.Repeat("+"+strings.Repeat("字", 3000)+"\n", patchMaxLines+10)
	b := ParsePatch(big)[0]
	if !b.Cut || len(b.Lines) != patchMaxLines || len([]rune(b.Lines[1].Text)) != patchMaxLine+1 {
		t.Fatalf("caps: cut=%v lines=%d", b.Cut, len(b.Lines))
	}
}

func TestRunChangesAPI(t *testing.T) {
	c := login(t)
	seedRun(t, c, "ch1", "fix", "succeeded", 0, time.Now())
	seedRun(t, c, "ch2", "read", "succeeded", 0, time.Now())
	fc := `{"files":[{"path":"a.txt","status":"M","add":1,"del":1},{"path":"bin.dat","status":"A","add":0,"del":0,"binary":true}],"count":4,"add":2,"del":1,"patch_cut":true}`
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET run_mode='edit', workspace_dir='/w', file_changes=?, file_patch=? WHERE id='ch1'`, fc, samplePatch); err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, map[string]any) {
		rec := c.do(http.MethodGet, path, "", nil)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	_, r1 := get("/api/runs/ch1")
	ch, ok := r1["file_changes"].(map[string]any)
	if !ok || ch["count"] != 4.0 || len(ch["files"].([]any)) != 2 {
		t.Fatalf("detail file_changes %v", r1["file_changes"])
	}
	if _, has := r1["file_patch"]; has {
		t.Fatal("patch in the run detail")
	}
	if _, r2 := get("/api/runs/ch2"); r2["file_changes"] != nil {
		t.Fatalf("read run has file_changes %v", r2["file_changes"])
	}
	code, d := get("/api/runs/ch1/changes")
	if code != 200 || d["patch"] != samplePatch || d["patch_cut"] != true || len(d["diffs"].([]any)) != 4 {
		t.Fatalf("changes %d %v", code, d)
	}
	if code, d := get("/api/runs/ch2/changes"); code != 200 || d["patch"] != "" || len(d["diffs"].([]any)) != 0 {
		t.Fatalf("no changes %d %v", code, d)
	}
	if code, _ := get("/api/runs/nope/changes"); code != 404 {
		t.Fatalf("missing %d", code)
	}
	if rec := c.do(http.MethodPost, "/api/runs/ch1/changes", "", nil); rec.Code == 200 {
		t.Fatal("POST accepted")
	}
	// signed out: refused
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/runs/ch1/changes", nil))
	if rr.Code != 401 {
		t.Fatalf("no session %d", rr.Code)
	}
}

func TestRunChangesUI(t *testing.T) {
	b, _ := os.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, s := range []string{
		"runChangesHtml(r) +",
		`if ($("#rd-chg")) bindRunChanges($("#rd-chg"), r);`,
		`api("/api/runs/" + enc(r.id) + "/changes")`,
		`saveBlob(new Blob([x.patch || ""], { type: "text/x-diff" }), r.id + ".diff")`,
		`<colgroup><col class="ln"><col class="ln"><col class="sg"><col></colgroup>`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, s := range []string{".rd-chg-list { max-height: min(480px, 60vh); overflow: auto;", ".rd-chg-body table.diff col.ln { width: 44px; }", "position: sticky; top: 0;"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		raw, _ := os.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for k, ph := range map[string]string{"rd.chg": "", "rd.chg_tip": "git", "rd.chg_none": "", "rd.chg_copy": "", "rd.chg_dl": ".diff", "rd.chg_dl_cut": "512", "rd.chg_bin": "", "rd.chg_more": "{n}", "rd.chg_cut": "", "rd.chg_mode": "", "rd.chg_noeol": ""} {
			if m[k] == "" || !strings.Contains(m[k], ph) {
				t.Errorf("%s %s = %q", f, k, m[k])
			}
		}
	}
	fn := regexp.MustCompile(`(?s)  function runChangesHtml\(r\) \{\n.*?\n  function bindRunChanges`).FindString(src)
	if fn == "" {
		t.Fatal("runChangesHtml not found")
	}
	fn = strings.TrimSuffix(fn, "  function bindRunChanges")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");}
function T(k,v){return k+(v?JSON.stringify(v):"");} function ic(n){return "<i>"+n+"</i>";} function num(n){return String(n);}
function skeleton(){return "";}
` + fn + `
var fail = function (m, h) { console.error(m + "\n" + h); process.exitCode = 1; };
var c = { files: [{ path: "<img src=x>.txt", status: "A", add: 2, del: 0 }, { path: "b", status: "D", add: 0, del: 3 }, { path: "p.png", status: "M", binary: true }], count: 5, add: 2, del: 3 };
if (runChangesHtml({ run_mode: "read", file_changes: c }) !== "") fail("read run", "");
if (runChangesHtml({ run_mode: "edit" }) !== "") fail("no changes field", "");
var h = runChangesHtml({ id: "r1", run_mode: "edit", file_changes: c });
if (h.indexOf("<img") >= 0 || h.indexOf("&lt;img src=x&gt;.txt") < 0) fail("path escaped", h);
if ((h.match(/data-chg="/g) || []).length !== 3) fail("rows", h);
if (h.indexOf('pill sm ok">diff.st_added') < 0 || h.indexOf('pill sm bad">diff.st_removed') < 0 || h.indexOf("rd.chg_bin") < 0) fail("status", h);
if (h.indexOf('rd.chg_more{"n":"2"}'.replace(/"/g, "&quot;")) < 0 && h.indexOf('rd.chg_more{&quot;n&quot;:&quot;2&quot;}') < 0) fail("more", h);
if (h.indexOf("data-chg-dl") < 0 || h.indexOf("rd.chg_dl_cut") >= 0) fail("download", h);
c.patch_cut = true;
if (runChangesHtml({ run_mode: "edit", file_changes: c }).indexOf("rd.chg_dl_cut") < 0) fail("cut tip", "");
var n = runChangesHtml({ run_mode: "edit", file_changes: { files: [], count: 0 } });
if (n.indexOf("rd.chg_none") < 0 || n.indexOf("data-chg") >= 0) fail("none", n);
var dh = chgDiffHtml({ lines: [{ op: "@", text: "@@ -1 +1 @@" }, { op: "-", a: 1, text: "<b>" }, { op: "+", b: 1, text: "x" }, { op: "\\", text: "No newline" }] });
if (dh.indexOf("<b>") >= 0 || dh.indexOf("&lt;b&gt;") < 0 || dh.indexOf("rd.chg_noeol") < 0 || dh.indexOf('class="del"') < 0) fail("diff", dh);
if (chgDiffHtml(undefined).indexOf("rd.chg_cut") < 0 || chgDiffHtml({ lines: [], binary: true }).indexOf("diff.st_binary") < 0 || chgDiffHtml({ lines: [] }).indexOf("rd.chg_mode") < 0) fail("empty diffs", "");
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("runChangesHtml: %v\n%s", err, out)
	}
}
