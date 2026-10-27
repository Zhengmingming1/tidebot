package webui

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestMarkdownPreviewUI: Markdown skill files open formatted with a preview / source switch,
// both languages, no native dialogs, and the renderer block stays free of lookbehind regexes
// (older WebKit in the desktop app rejects the whole script on one).
func TestMarkdownPreviewUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`function fileViewHtml(f, mode) {`,
		`'<div class="fb-md md" tabindex="0">' + mdHtml(f.text, f.path.split("/").slice(0, -1).join("/"))`,
		`localStorage.getItem("tb_md_view") === "source"`,
		`a[data-md-file], a[data-md-anchor]`,
		`toast(T("fb.no_file", { p: p }), true)`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	block := mdBlock(t, src)
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "(?<", "innerHTML", "<img", "<script"} {
		if strings.Contains(block, bad) {
			t.Errorf("markdown block uses %s", bad)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"fb.view_as", "fb.preview", "fb.source", "fb.preview_tip", "fb.source_tip", "fb.no_file"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".fb-md { flex: 1; min-height: 0; overflow: auto;", ".md pre.md-code {", ".md .md-fm {", ".fb-md, .tx-list,"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles lack %s", s)
		}
	}
}

func mdBlock(t *testing.T, src string) string {
	t.Helper()
	i := strings.Index(src, "  // ---------- Markdown preview (skill files) ----------")
	j := strings.Index(src, "  // ---------- end Markdown preview ----------")
	if i < 0 || j < i {
		t.Fatal("markdown block not found")
	}
	return src[i:j]
}

// TestMarkdownRender runs the renderer in node: formatting, front matter, links (only safe
// schemes, relative paths resolved against the file's folder), and nothing from the file
// reaching the page as markup.
func TestMarkdownRender(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	block := mdBlock(t, src)
	esc := regexpFunc(t, src, "  function esc(v) {")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := esc + block + `
var fails = 0;
function has(name, html, want) { if (html.indexOf(want) < 0) { console.error(name + ": want " + want + "\n got " + html); fails++; } }
function lacks(name, html, bad) { if (html.indexOf(bad) >= 0) { console.error(name + ": must not contain " + bad + "\n got " + html); fails++; } }
var doc = "---\r\nname: pdf-tools\r\ndescription: >\r\n  Fill PDFs.\r\n  Merge them.\r\n---\r\n# PDF Tools\r\nUse *this* and **that** and ` + "`code <b>`" + `.\r\n";
var h = mdHtml(doc, "");
has("front matter", h, '<table class="md-fm"><tbody><tr><th>name</th><td>pdf-tools</td></tr><tr><th>description</th><td>Fill PDFs. Merge them.</td></tr>');
has("heading", h, '<h1 data-md-id="pdf-tools">PDF Tools</h1>');
has("inline", h, "Use <em>this</em> and <strong>that</strong> and <code>code &lt;b&gt;</code>.");
h = mdHtml("- [ ] todo\n- [x] done\n  - sub\n\n1. one\n2. two\n\n3. three", "");
has("task", h, '<li class="md-task"><span class="md-box" aria-hidden="true"></span>todo</li>');
has("task done", h, '<li class="md-task done">');
has("nested", h, "<ul><li>sub</li></ul>");
has("ordered", h, "<ol><li><p>one</p></li><li><p>two</p></li><li><p>three</p></li></ol>");
h = mdHtml("| a | b \\| c | n |\n|:--|:-:|--:|\n| 1 | 2 | 3 |", "");
has("table", h, '<div class="md-tbl"><table><thead><tr><th>a</th><th class="md-c">b | c</th><th class="md-r">n</th></tr></thead><tbody><tr><td>1</td><td class="md-c">2</td><td class="md-r">3</td></tr></tbody></table></div>');
h = mdHtml("` + "```" + `sh\necho \"<x>\" && rm\n` + "```" + `\n\n> quote\n\n---\n\nTitle\n===", "");
has("fence", h, '<pre class="md-code"><span class="md-lang">sh</span><code>echo &quot;&lt;x&gt;&quot; &amp;&amp; rm</code></pre>');
has("quote", h, "<blockquote><p>quote</p></blockquote><hr>");
has("setext", h, '<h1 data-md-id="title">Title</h1>');
h = mdHtml("[f](forms.md#x) [up](../b/c.md) [top](#PDF%20Tools) [w](https://e.com/a_(b)) <https://e.com/q?a=1&b=2> see https://e.com/x.", "refs");
has("relative", h, 'data-md-file="refs/forms.md"');
has("parent", h, 'data-md-file="b/c.md"');
has("anchor", h, 'data-md-anchor="pdf-tools"');
has("parens", h, 'href="https://e.com/a_(b)" target="_blank" rel="noopener noreferrer"');
has("autolink", h, 'href="https://e.com/q?a=1&amp;b=2"');
has("bare", h, '<a href="https://e.com/x" target="_blank"');
h = mdHtml("<img src=x onerror=alert(1)> <script>alert(1)</script>\n[a](javascript:alert(1)) [b](data:text/html,x) [c](//evil.com) [d](/etc/passwd) [e\" onmouseover=\"x](https://e.com/\"x)\n![p](https://track.example/p.png) [z](JaVaScRiPt:alert(1))", "");
["<img", "<script", "javascript:", "data:", "evil.com", "/etc/passwd", "onmouseover=\"", "track.example/p.png\" src"].forEach(function (b) { lacks("xss", h, b); });
has("raw html kept as text", h, "&lt;img src=x onerror=alert(1)&gt; &lt;script&gt;");
has("image as link", h, '>🖼 p</a>');
has("bad link keeps text", h, "\na b c d <a");
h = mdHtml("a\\*b\\* ` + "`x\\\\*y`" + ` line  \nnext", "");
has("escapes", h, "a*b* <code>x\\*y</code> line<br>next");
lacks("no stray nul", mdHtml("x\u0000 0\u0000 y", ""), "undefined");
if (fails) process.exitCode = 1;
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("mdHtml: %v\n%s", err, out)
	}
}

// regexpFunc returns the one-line-bodied function starting at head up to its closing "  }".
func regexpFunc(t *testing.T, src, head string) string {
	t.Helper()
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("%s not found", head)
	}
	j := strings.Index(src[i:], "\n  }\n")
	if j < 0 {
		t.Fatalf("%s: end not found", head)
	}
	return src[i:i+j+5] + "\n"
}
