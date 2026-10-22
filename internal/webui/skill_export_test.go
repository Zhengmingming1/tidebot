package webui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func uploadZip(t *testing.T, c *client, name string, data []byte) skillPreview {
	t.Helper()
	var mb bytes.Buffer
	mw := multipart.NewWriter(&mb)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	rr := c.do(http.MethodPost, "/api/skills/upload", mw.FormDataContentType(), mb.Bytes())
	if rr.Code != 200 {
		t.Fatalf("upload %d %s", rr.Code, rr.Body.String())
	}
	var p skillPreview
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	return p
}

// TestSkillExport: a skill version downloads as a zip under one portable folder, bytes
// as stored (CRLF kept), the same bytes every time, and Import takes it back as is.
func TestSkillExport(t *testing.T) {
	c := login(t)
	body, ct := multipartZip(t, map[string]string{"pdf/SKILL.md": "---\r\nname: \"PDF 工具.split\"\r\ndescription: Split PDFs\r\n---\r\n# hi\r\n", "pdf/scripts/run.sh": "echo 1\n"})
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
	id := sl[0].ID

	rr = c.do(http.MethodGet, "/api/skills/"+id+"/export", "", nil)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/zip" || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export %d %v", rr.Code, rr.Header())
	}
	cd := rr.Header().Get("Content-Disposition")
	want := "PDF-工具-split-v1.zip"
	if !strings.Contains(cd, `filename="PDF-__-split-v1.zip"`) || !strings.Contains(cd, "filename*=UTF-8''"+url.PathEscape(want)) {
		t.Fatalf("disposition %q", cd)
	}
	data := rr.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}
	if len(got) != 2 || !strings.Contains(got["PDF-工具-split/SKILL.md"], "---\r\nname:") || got["PDF-工具-split/scripts/run.sh"] != "echo 1\n" {
		t.Fatalf("entries %q", got)
	}
	if again := c.do(http.MethodGet, "/api/skills/"+id+"/export", "", nil).Body.Bytes(); !bytes.Equal(again, data) {
		t.Fatal("export is not deterministic")
	}

	// round trip: the download re-imports as the same skill (new version).
	p2 := uploadZip(t, c, want, data)
	if p2.Name != "PDF 工具.split" || p2.Files != 2 || p2.Existing == nil {
		t.Fatalf("re-upload preview %+v", p2)
	}
	if code, _ := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p2.StageID, "name": p2.Name, "mode": "version"}); code != 200 {
		t.Fatalf("re-import %d", code)
	}

	// a named version; foreign / bogus versions and unknown skills are refused.
	var vers struct {
		Versions []versionDTO `json:"versions"`
	}
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/skills/"+id+"/versions", "", nil).Body.Bytes(), &vers)
	if len(vers.Versions) < 1 {
		t.Fatal("no versions")
	}
	if rr = c.do(http.MethodGet, "/api/skills/"+id+"/export?v="+vers.Versions[0].ID, "", nil); rr.Code != 200 {
		t.Fatalf("export v %d", rr.Code)
	}
	if rr = c.do(http.MethodGet, "/api/skills/"+id+"/export?v=nope", "", nil); rr.Code != 400 {
		t.Fatalf("bogus v %d", rr.Code)
	}
	if rr = c.do(http.MethodGet, "/api/skills/nope/export", "", nil); rr.Code != 404 {
		t.Fatalf("unknown skill %d", rr.Code)
	}
	if rr = c.do(http.MethodPost, "/api/skills/"+id+"/export", "", nil); rr.Code != 405 {
		t.Fatalf("post %d", rr.Code)
	}
	// signed out → 401
	anon := &client{t: t, s: c.s}
	if rr = anon.do(http.MethodGet, "/api/skills/"+id+"/export", "", nil); rr.Code != 401 {
		t.Fatalf("anon %d", rr.Code)
	}
}

func TestExportSlug(t *testing.T) {
	for in, want := range map[string]string{
		"PDF tools":        "PDF-tools",
		`a/b\c:d*?"<>|e`:   "a-b-c-d-e",
		"  每日 报告.  ":       "每日-报告",
		"../..":            "skill",
		"":                 "skill",
		"repo_summary-2.0": "repo_summary-2-0",
		"con":              "con-skill",
		"COM1":             "COM1-skill",
	} {
		if got := exportSlug(in); got != want {
			t.Errorf("exportSlug(%q) = %q, want %q", in, got, want)
		}
	}
	if n := len([]rune(exportSlug(strings.Repeat("长", 200)))); n != 60 {
		t.Errorf("slug length %d", n)
	}
}

// TestSkillExportUI: download buttons on the list, the detail head and each version;
// filename* (UTF-8) wins over the ASCII fallback in the browser too.
func TestSkillExportUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`data-sk-export="' + esc(s.id) + '"`,
		`data-v-export="' + esc(v.id) + '"`,
		`id="sk-export"`,
		`exportSkill(id, bt.getAttribute("data-v-export"), bt)`,
		`saveBlob(b, dispName(res.headers.get("content-disposition"), fallback))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	fn := regexp.MustCompile(`(?s)  function dispName\(cd, fallback\) \{.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("dispName not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := fn + `
var cases = [
  ["attachment; filename=\"PDF-__-v1.zip\"; filename*=UTF-8''PDF-%E5%B7%A5%E5%85%B7-v1.zip", "PDF-工具-v1.zip"],
  ["attachment; filename=\"a.csv\"", "a.csv"],
  ["attachment; filename=\"x.zip\"; filename*=UTF-8''%E0%A4%A", "x.zip"],
  ["", "fb.bin"], [null, "fb.bin"]];
cases.forEach(function (c) { var g = dispName(c[0], "fb.bin"); if (g !== c[1]) { console.log("bad", c[0], g); process.exitCode = 1; } });
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
}
