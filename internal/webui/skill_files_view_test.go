package webui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestSkillFileView: one file of a version comes back for the file browser — text as
// stored (CRLF kept, line count and line-ending kind), binary and oversized files
// without content, and only paths that are part of that version.
func TestSkillFileView(t *testing.T) {
	c := login(t)
	big := strings.Repeat("0123456789abcdef\n", (diffMaxFileBytes/17)+10)
	body, ct := multipartZip(t, map[string]string{
		"fv/SKILL.md":       "---\r\nname: 文件浏览\r\ndescription: d\r\n---\r\n# hi\r\n",
		"fv/scripts/run.sh": "echo 1\necho 2",
		"fv/assets/a.bin":   "PNG\x00\x01\x02",
		"fv/data/big.txt":   big,
		"fv/empty.txt":      "",
	})
	rr := c.do(http.MethodPost, "/api/skills/upload", ct, body)
	var p skillPreview
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if code, out := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name}); code != 200 {
		t.Fatalf("import %d %v", code, out)
	}
	var sl []skillDTO
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/skills", "", nil).Body.Bytes(), &sl)
	if len(sl) != 1 {
		t.Fatalf("skills %+v", sl)
	}
	sid := sl[0].ID
	_, vs := c.json(http.MethodGet, "/api/skills/"+sid+"/versions", nil)
	vid := vs["current"].(string)

	get := func(path string) (int, skillFileView) {
		rr := c.do(http.MethodGet, "/api/skills/"+sid+"/file?v="+url.QueryEscape(vid)+"&path="+url.QueryEscape(path), "", nil)
		var f skillFileView
		_ = json.Unmarshal(rr.Body.Bytes(), &f)
		return rr.Code, f
	}
	var files []struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/skills/"+sid+"/files?v="+vid, "", nil).Body.Bytes(), &files)
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.Path] = true
	}
	for _, want := range []string{"SKILL.md", "scripts/run.sh", "assets/a.bin", "data/big.txt"} {
		if !seen[want] {
			t.Fatalf("files %+v lacks %s", files, want)
		}
	}

	if code, f := get("SKILL.md"); code != 200 || f.Kind != "text" || f.EOL != "crlf" || f.Lines != 5 || !strings.HasPrefix(f.Text, "---\r\nname: 文件浏览") {
		t.Fatalf("SKILL.md %d %+v", code, f)
	}
	if code, f := get("scripts/run.sh"); code != 200 || f.Kind != "text" || f.EOL != "lf" || f.Lines != 2 || f.Text != "echo 1\necho 2" {
		t.Fatalf("run.sh %d %+v", code, f)
	}
	if code, f := get("assets/a.bin"); code != 200 || f.Kind != "binary" || f.Text != "" || f.Size != 6 {
		t.Fatalf("binary %d %+v", code, f)
	}
	if code, f := get("data/big.txt"); code != 200 || f.Kind != "too_large" || f.Text != "" || f.Size != len(big) {
		t.Fatalf("big %d kind=%s size=%d", code, f.Kind, f.Size)
	}
	if seen["empty.txt"] {
		if code, f := get("empty.txt"); code != 200 || f.Kind != "text" || f.Lines != 0 || f.EOL != "" {
			t.Fatalf("empty %d %+v", code, f)
		}
	}
	// only files of that version: no traversal, no absolute paths, no made-up names
	for _, bad := range []string{"../SKILL.md", "/etc/passwd", "scripts/../SKILL.md", "nope.md", `scripts\run.sh`} {
		if code, _ := get(bad); code != 404 {
			t.Errorf("%q → %d, want 404", bad, code)
		}
	}
	if code, _ := get(""); code != 400 {
		t.Errorf("empty path → %d", code)
	}
	if rr := c.do(http.MethodGet, "/api/skills/"+sid+"/file?v=junk&path=SKILL.md", "", nil); rr.Code != 400 {
		t.Errorf("foreign version %d", rr.Code)
	}
	if rr := c.do(http.MethodGet, "/api/skills/other/file?v="+vid+"&path=SKILL.md", "", nil); rr.Code != 400 {
		t.Errorf("wrong skill %d", rr.Code)
	}
	if rr := c.do(http.MethodPost, "/api/skills/"+sid+"/file?v="+vid+"&path=SKILL.md", "", nil); rr.Code != 405 && rr.Code != 403 {
		t.Errorf("POST %d", rr.Code)
	}
	// the JSON escapes markup, the page escapes again before inserting
	if rr := c.do(http.MethodGet, "/api/skills/"+sid+"/file?v="+vid+"&path=SKILL.md", "", nil); strings.Contains(rr.Body.String(), "<") {
		t.Errorf("raw markup in JSON")
	}
}

func TestViewSkillFileEOL(t *testing.T) {
	for _, c := range []struct {
		in    string
		eol   string
		lines int
	}{{"a", "", 1}, {"a\n", "lf", 1}, {"a\r\nb", "crlf", 2}, {"a\r\nb\n", "mixed", 2}, {"", "", 0}} {
		f := viewSkillFile("x", []byte(c.in))
		if f.Kind != "text" || f.EOL != c.eol || f.Lines != c.lines {
			t.Errorf("%q → %+v", c.in, f)
		}
	}
}

// TestSkillFileBrowserUI: the detail page has the compare / files tabs, reads files through
// the new endpoint, escapes the text, keeps the view in the address and has both locales.
func TestSkillFileBrowserUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, s := range []string{`data-sktab="`, `"/file?v=" + enc(fv) + "&path=" + enc(p)`, `"&tab=files&v="`, `params.get("tab") === "files"`,
		`data-v-files="`, `function browseTree(`, `function treeKeys(`, `<pre class="fb-tx" tabindex="0">' + esc(text)`, `if (g !== fgen) return;`} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".fb {", ".fb-tree", ".fb-code", ".fb-view.busy"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for _, k := range []string{"sk.tab_diff", "sk.tab_files", "fb.eol_lf", "fb.eol_crlf", "fb.eol_mixed", "fb.binary", "fb.too_large", "fb.view_v"} {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
}
