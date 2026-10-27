package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRunAnswerAPI: the run detail carries the stored final answer (and the cut flag); the
// runs list does not, so lists stay small.
func TestRunAnswerAPI(t *testing.T) {
	c := login(t)
	seedRun(t, c, "ra1", "summarize", "succeeded", 0.01, time.Now())
	seedRun(t, c, "ra2", "old run", "succeeded", 0.01, time.Now())
	if _, err := c.s.deps.DB.ExecContext(context.Background(), `UPDATE runs SET result_text=?, result_truncated=1 WHERE id='ra1'`, "## 摘要\n\n- <b>一</b>"); err != nil {
		t.Fatal(err)
	}
	get := func(id string) map[string]any {
		rec := c.do(http.MethodGet, "/api/runs/"+id, "", nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", id, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	if m := get("ra1"); m["result"] != "## 摘要\n\n- <b>一</b>" || m["result_truncated"] != true {
		t.Fatalf("ra1 %v", m)
	}
	if m := get("ra2"); m["result"] != "" || m["result_truncated"] != nil {
		t.Fatalf("ra2 %v", m)
	}
	for _, r := range listRuns(t, c, "") {
		if _, ok := r["result"]; ok {
			t.Fatalf("list carries result: %v", r)
		}
	}
}

// TestRunAnswerUI: the card is wired into the run page with preview / source, copy and
// download, strings in both languages, its own scroll box, and no native dialogs.
func TestRunAnswerUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`runAnswerHtml(r, ansMode()) +
        costCalcHtml(r) +`,
		`if ($("#rd-ans")) bindRunAnswer($("#rd-ans"), r);`,
		`'<div class="rd-ans-body md" tabindex="0">' + mdHtml(r.result, null) + "</div>"`,
		`'<pre class="rd-ans-body rd-ans-src" tabindex="0">' + esc(r.result) + "</pre>"`,
		`localStorage.getItem("tb_ans_view") === "source"`,
		`copyText(r.result).catch(fail)`,
		`saveBlob(new Blob([r.result.replace(/\n?$/, "\n")], { type: "text/markdown" }), r.id + ".md")`,
		`if (base === null) return null;`,
		`if (lines[0] === "---" && base !== null) {`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	i, j := strings.Index(src, "  function ansMode() {"), strings.Index(src, "  // Run detail stat cards.")
	if i < 0 || j < i {
		t.Fatal("answer block not found")
	}
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "(?<"} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("answer block uses %s", bad)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"rd.answer", "rd.ans_meta", "rd.ans_preview_tip", "rd.ans_source_tip", "rd.ans_copy", "rd.ans_dl", "rd.ans_cut", "rd.ans_cut_tip"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".rd-ans-body { max-height: min(320px, 40vh); overflow: auto;", ".rd-ans-src {", ".rd-ans-body, .desc-box"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles lack %s", s)
		}
	}
}

// TestRunAnswerRender: in an answer (base null) relative links and front matter are not
// interpreted, web links and in-page anchors still work, and markup stays escaped.
func TestRunAnswerRender(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	block := mdBlock(t, src)
	esc := regexpFunc(t, src, "  function esc(v) {")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := esc + block + `
var h = mdHtml("---\nnot: front matter\n---\n## 结果\nSee [notes](notes.md), [site](https://example.com), [top](#结果) and <script>x</script>.\n", null);
var bad = [];
if (h.indexOf("md-fm") >= 0) bad.push("front matter table");
if (h.indexOf("data-md-file") >= 0) bad.push("relative file link");
if (h.indexOf('href="https://example.com"') < 0) bad.push("web link");
if (h.indexOf('data-md-anchor="结果"') < 0) bad.push("anchor");
if (h.indexOf("<script>") >= 0 || h.indexOf("&lt;script&gt;") < 0) bad.push("escaping");
if (h.indexOf("<hr>") < 0) bad.push("rule");
var k = mdHtml("[ref](reference/a.md)", "");
if (k.indexOf('data-md-file="reference/a.md"') < 0) bad.push("skill links still resolve");
if (bad.length) { console.error(bad.join(", ") + "\n" + h); process.exit(1); }
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
