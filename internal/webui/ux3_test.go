package webui

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

type client struct {
	t    *testing.T
	s    *Server
	sid  string
	csrf string
}

func login(t *testing.T) *client {
	t.Helper()
	s, _ := testServer(t)
	s.deps.Cfg.DataDir = t.TempDir()
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"testpass99"}`)))
	if rr.Code != 200 {
		t.Fatalf("login %d", rr.Code)
	}
	var lr map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &lr)
	c := &client{t: t, s: s, csrf: lr["csrf_token"].(string)}
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == auth.SessionCookie {
			c.sid = ck.Value
		}
	}
	return c
}

func (c *client) do(method, path, ctype string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	req.Header.Set(auth.CSRFHeader, c.csrf)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	return rr
}

func (c *client) json(method, path string, v any) (int, map[string]any) {
	var b []byte
	if v != nil {
		b, _ = json.Marshal(v)
	}
	rr := c.do(method, path, "application/json", b)
	out := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestNewButtonID(t *testing.T) {
	taken := map[string]bool{"daily-report": true}
	if got := NewButtonID("Daily Report", taken); !strings.HasPrefix(got, "daily-report-") || taken[got] {
		t.Fatalf("got %q", got)
	}
	if got := NewButtonID("Sync now!", nil); got != "sync-now" {
		t.Fatalf("got %q", got)
	}
	a, b := NewButtonID("每日报告", nil), NewButtonID("每日报告", nil)
	if !strings.HasPrefix(a, "btn-") || a == b {
		t.Fatalf("non-ascii ids %q %q", a, b)
	}
}

func TestValidateBaseURLAndEnv(t *testing.T) {
	for _, ok := range []string{"", "https://api.example.com/anthropic", "http://localhost:8080", "http://127.0.0.1:9/x"} {
		if err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com", "ftp://x", "https://user:pw@x.com", "x.com"} {
		if ValidateBaseURL(bad) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if ValidateExtraEnv(map[string]string{"API_TIMEOUT_MS": "1", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "200000", "MAX_THINKING_TOKENS": "1"}) != nil {
		t.Error("plain env rejected")
	}
	for _, k := range []string{"MY_API_KEY", "ANTHROPIC_AUTH_TOKEN", "lower", "ANTHROPIC_BASE_URL"} {
		if ValidateExtraEnv(map[string]string{k: "x"}) == nil {
			t.Errorf("%s accepted", k)
		}
	}
}

func TestButtonsAutoID(t *testing.T) {
	c := login(t)
	code, _ := c.json(http.MethodPut, "/api/buttons", []map[string]any{{"label": "Daily Report", "prompt": "hi {{who}}", "params": []map[string]string{{"name": "who", "default": "me"}}, "color": "#2f6fed"}, {"label": "Daily Report", "prompt": "x"}})
	if code != 200 {
		t.Fatalf("put %d", code)
	}
	rr := c.do(http.MethodGet, "/api/buttons", "", nil)
	var list []buttonDTO
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if len(list) != 2 || list[0].ID != "daily-report" || !strings.HasPrefix(list[1].ID, "daily-report-") || list[0].Color != "#2f6fed" {
		t.Fatalf("ids %+v", list)
	}
	if code, _ := c.json(http.MethodPut, "/api/buttons", []map[string]any{{"label": "x", "prompt": "x", "color": "red;}"}}); code != 400 {
		t.Fatalf("bad color accepted: %d", code)
	}
}

func TestTaskCRUDFromWebUI(t *testing.T) {
	c := login(t)
	base := map[string]any{"name": "Morning", "prompt": "summarize", "provider": "anthropic", "cron": "0 9 * * 1-5", "budget_usd": 1.5, "enabled": true, "timeout_minutes": 20}
	for name, mut := range map[string]func(m map[string]any){
		"empty name":   func(m map[string]any) { m["name"] = " " },
		"bad cron":     func(m map[string]any) { m["cron"] = "every day" },
		"bad provider": func(m map[string]any) { m["provider"] = "nope'; DROP TABLE tasks;--" },
		"bad skill":    func(m map[string]any) { m["skill_id"] = "x' OR '1'='1" },
		"neg budget":   func(m map[string]any) { m["budget_usd"] = -1 },
	} {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		mut(m)
		if code, _ := c.json(http.MethodPost, "/api/tasks", m); code != 400 {
			t.Errorf("%s: got %d", name, code)
		}
	}
	code, tk := c.json(http.MethodPost, "/api/tasks", base)
	if code != 201 {
		t.Fatalf("create %d %v", code, tk)
	}
	id := tk["id"].(string)
	_, f := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	form := f["form"].(map[string]any)
	if form["cron"] != "0 9 * * 1-5" || form["budget_usd"].(float64) != 1.5 || form["enabled"] != true {
		t.Fatalf("form %v", form)
	}
	base["name"], base["enabled"], base["cron"] = "Morning 2", false, ""
	if code, out := c.json(http.MethodPut, "/api/tasks/"+id, base); code != 200 {
		t.Fatalf("update %d %v", code, out)
	}
	_, got := c.json(http.MethodGet, "/api/tasks/"+id, nil)
	if got["name"] != "Morning 2" || got["status"] != "paused" {
		t.Fatalf("after update %v", got)
	}
	if code, _ := c.json(http.MethodDelete, "/api/tasks/"+id, nil); code != 200 {
		t.Fatalf("delete %d", code)
	}
	rr := c.do(http.MethodGet, "/api/tasks", "", nil)
	if strings.Contains(rr.Body.String(), id) {
		t.Fatal("deleted task still listed")
	}
}

func TestCronPreview(t *testing.T) {
	c := login(t)
	_, out := c.json(http.MethodGet, "/api/schedule/preview?cron=0+9+*+*+*", nil)
	if ts, _ := out["times"].([]any); len(ts) != 5 {
		t.Fatalf("preview %v", out)
	}
	if code, _ := c.json(http.MethodGet, "/api/schedule/preview?cron=bogus", nil); code != 400 {
		t.Fatalf("bogus cron %d", code)
	}
}

func multipartZip(t *testing.T, files map[string]string) ([]byte, string) {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for n, body := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	var mb bytes.Buffer
	mw := multipart.NewWriter(&mb)
	fw, _ := mw.CreateFormFile("file", "skill.zip")
	_, _ = fw.Write(zb.Bytes())
	_ = mw.Close()
	return mb.Bytes(), mw.FormDataContentType()
}

func TestSkillUploadAndImport(t *testing.T) {
	c := login(t)
	body, ct := multipartZip(t, map[string]string{"pdf-tools/SKILL.md": "---\nname: PDF tools\ndescription: Split and merge PDFs\n---\n# hi\n", "pdf-tools/run.sh": "echo"})
	rr := c.do(http.MethodPost, "/api/skills/upload", ct, body)
	if rr.Code != 200 {
		t.Fatalf("upload %d %s", rr.Code, rr.Body.String())
	}
	var p skillPreview
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Name != "PDF tools" || p.Description != "Split and merge PDFs" || p.Files != 2 || p.Existing != nil {
		t.Fatalf("preview %+v", p)
	}
	if code, out := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name}); code != 200 {
		t.Fatalf("import %d %v", code, out)
	}
	var sl []skillDTO
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/skills", "", nil).Body.Bytes(), &sl)
	if len(sl) != 1 || sl[0].Description != "Split and merge PDFs" {
		t.Fatalf("skill list description %+v", sl)
	}
	// same name again → 409 with existing, then import as a new version
	rr = c.do(http.MethodPost, "/api/skills/upload", ct, body)
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Existing == nil || p.Existing.VersionNo != 1 {
		t.Fatalf("existing not reported %+v", p)
	}
	if code, _ := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name}); code != 409 {
		t.Fatalf("want 409 got %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name, "mode": "version"}); code != 200 {
		t.Fatalf("version import %d", code)
	}
	// missing SKILL.md and zip-slip are rejected with clear errors
	body, ct = multipartZip(t, map[string]string{"readme.txt": "x"})
	rr = c.do(http.MethodPost, "/api/skills/upload", ct, body)
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "SKILL.md") {
		t.Fatalf("no skill.md: %d %s", rr.Code, rr.Body.String())
	}
	body, ct = multipartZip(t, map[string]string{"../evil/SKILL.md": "x"})
	if rr = c.do(http.MethodPost, "/api/skills/upload", ct, body); rr.Code != 400 {
		t.Fatalf("zip slip: %d", rr.Code)
	}
	// stage ids are opaque hex; path tricks are refused
	if code, _ := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": "../../etc", "name": "x"}); code != 400 {
		t.Fatalf("stage traversal %d", code)
	}
	// uploads need CSRF
	req := httptest.NewRequest(http.MethodPost, "/api/skills/upload", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	req.Header.Set("Content-Type", ct)
	rr = httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
	_ = context.Background()
}
