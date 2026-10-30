package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

var regexpNative = regexp.MustCompile(`\b(window\.)?(confirm|alert|prompt)\(`)

func readDict(t *testing.T, lang string) map[string]string {
	t.Helper()
	b, err := Static.ReadFile("static/i18n/" + lang + ".json")
	if err != nil {
		t.Fatal(err)
	}
	d := map[string]string{}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func centerIDs(t *testing.T, c *client) map[string]bool {
	t.Helper()
	code, out := c.json(http.MethodGet, "/api/notifications/center", nil)
	if code != 200 {
		t.Fatalf("center %d", code)
	}
	ids := map[string]bool{}
	for _, x := range out["items"].([]any) {
		ids[x.(map[string]any)["id"].(string)] = true
	}
	return ids
}

// TestNotifClear: one row, everything up to the "read" mark, and undo of exactly one
// clear; cleared rows stay in the table, bad input and missing CSRF are refused.
func TestNotifClear(t *testing.T) {
	c := login(t)
	db := c.s.deps.DB
	base := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	at := func(m int) string { return base.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	_, err := db.Exec(`INSERT INTO notifications(id,event,body,created_at,msg_key,msg_params) VALUES
		('a','update_available','x',?,'update_available','{"v":"0.1.8","detail":"## 新增\n- **主题**：石墨"}'),
		('b','run_failed','x',?,'run_failed','{}'),
		('c','run_finished','x',?,'run_finished','{}'),
		('d','run_finished','x',?,'run_finished','{}')`, at(0), at(1), at(2), at(3))
	if err != nil {
		t.Fatal(err)
	}
	// one row
	code, out := c.json(http.MethodPost, "/api/notifications/clear", map[string]any{"ids": []string{"b"}})
	if code != 200 || out["cleared"].(float64) != 1 || out["batch"] == nil {
		t.Fatalf("clear one %d %v", code, out)
	}
	one := out["batch"].(string)
	if ids := centerIDs(t, c); ids["b"] || !ids["a"] || len(ids) != 3 {
		t.Fatalf("after clear one %v", ids)
	}
	// everything seen up to c (inclusive); fractional marks are fine
	code, out = c.json(http.MethodPost, "/api/notifications/clear", map[string]any{"read_before": base.Add(2*time.Minute + 300*time.Millisecond).Format(time.RFC3339Nano)})
	if code != 200 || out["cleared"].(float64) != 2 {
		t.Fatalf("clear read %d %v", code, out)
	}
	read := out["batch"].(string)
	if ids := centerIDs(t, c); len(ids) != 1 || !ids["d"] {
		t.Fatalf("after clear read %v", ids)
	}
	// nothing left to clear: no batch, no toast
	code, out = c.json(http.MethodPost, "/api/notifications/clear", map[string]any{"read_before": at(2)})
	if code != 200 || out["cleared"].(float64) != 0 || out["batch"] != nil {
		t.Fatalf("clear again %d %v", code, out)
	}
	// undo brings back exactly that batch
	code, out = c.json(http.MethodPost, "/api/notifications/clear", map[string]any{"undo": read})
	if code != 200 || out["restored"].(float64) != 2 {
		t.Fatalf("undo %d %v", code, out)
	}
	if ids := centerIDs(t, c); len(ids) != 3 || ids["b"] {
		t.Fatalf("after undo %v", ids)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(1) FROM notifications WHERE cleared_at=?`, one).Scan(&n)
	if n != 1 {
		t.Fatalf("row b kept in the log as cleared: %d", n)
	}
	// legacy list hides cleared rows too
	rr := c.do(http.MethodGet, "/api/notifications", "", nil)
	if strings.Contains(rr.Body.String(), `"id":"b"`) {
		t.Fatalf("legacy list shows a cleared row: %s", rr.Body.String())
	}
	for _, bad := range []map[string]any{
		{}, {"ids": []string{"x' OR 1=1 --"}}, {"read_before": "yesterday"}, {"undo": "../x"},
		{"ids": make([]string, 101)},
	} {
		if code, _ := c.json(http.MethodPost, "/api/notifications/clear", bad); code != 400 {
			t.Fatalf("bad %v accepted: %d", bad, code)
		}
	}
	if code, _ := c.json(http.MethodGet, "/api/notifications/clear", nil); code != 405 {
		t.Fatalf("GET %d", code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/clear", bytes.NewReader([]byte(`{"read_before":"2030-01-01T00:00:00Z"}`)))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr = httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
	if ids := centerIDs(t, c); len(ids) != 3 {
		t.Fatalf("csrf-less request cleared rows: %v", ids)
	}
}

// TestNotifClearUI: the bell offers "clear read" and a per-row × (outside the link,
// labelled, revealed on focus and always on touch), clears with an undo toast and no
// native dialogs; update notices show a plain summary and open the update card; both
// languages carry every string.
func TestNotifClearUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	css, _ := Static.ReadFile("static/styles.css")
	src := string(js)
	for _, s := range []string{
		`id="nc-clear"`, `class="nc-x" data-clear="`, `clearNotifs(pop, { read_before: state.notifSeen })`,
		`"/api/notifications/clear", { method: "POST", body: { undo: r.batch } }`, `toastUndo(T(body.ids ? "nc.cleared_one" : "nc.cleared"`,
		`/^update_/.test(n.key || "") ? "#/settings/about/update"`, `'<div class="nc-row">' + row + '<button`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js missing %q", s)
		}
	}
	start := strings.Index(src, "  function clearNotifs(")
	end := strings.Index(src, "  function toggleNotif(")
	if start < 0 || end < start || regexpNative.MatchString(src[start:end]) {
		t.Error("clearNotifs must not use native dialogs")
	}
	for _, s := range []string{`.nc-row:hover .nc-x, .nc-row:focus-within .nc-x { opacity: 1; }`, `@media (hover: none) { .nc-row .nc-x { opacity: 1; } }`, `.nc-row > .item { padding-right: 32px; }`} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css missing %q", s)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		d := readDict(t, lang)
		for _, k := range []string{"nc.clear_one", "nc.clear_read", "nc.clear_read_tip", "nc.cleared", "nc.cleared_one", "nc.restored", "nc.more_items"} {
			if strings.TrimSpace(d[k]) == "" {
				t.Errorf("%s missing %s", lang, k)
			}
		}
	}
}

// TestNotesSummary runs the release-notes helpers: Markdown syntax never reaches the bell,
// bold lead-ins win, three items at most, CRLF notes read the same as LF.
func TestNotesSummary(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := string(js)
	a, b := strings.Index(src, "  function notesPlain("), strings.Index(src, "  function notifTitle(")
	if a < 0 || b < a {
		t.Fatal("notesPlain / notesSummary not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function T(k,v){return k==="nc.more_items"?"+"+v.n:k;}
` + src[a:b] + `
var bad = function (m, h) { console.error(m + "\n" + h); process.exitCode = 1; };
var md = "# tidebot v0.1.9\n\n这是一个小版本。\n\n## 新界面 / New look\n- **默认主题「石墨」**：白底、深色主按钮。\n  New default theme Graphite.\n- 顶部多了一条标题栏，显示面包屑；首页改版\n- 修复 [链接](https://example.com/x) 和 ` + "`code`" + `。\n* **四**：x\n";
var s = notesSummary(md);
if (s !== "默认主题「石墨」 · 顶部多了一条标题栏 · 修复 链接 和 code +1") bad("summary", s);
if (notesSummary(md.replace(/\n/g, "\r\n")) !== s) bad("crlf", notesSummary(md.replace(/\n/g, "\r\n")));
var p = notesPlain(md);
if (/[#*` + "`" + `\[\]()]|https?:/.test(p)) bad("plain keeps markdown", p);
if (p.split("\n")[0] !== "tidebot v0.1.9") bad("plain first line", p);
if (notesSummary("Rollback: health check failed") !== "Rollback: health check failed") bad("no list", notesSummary("Rollback: health check failed"));
if (notesSummary("") !== "") bad("empty", notesSummary(""));
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("notesSummary: %v\n%s", err, out)
	}
}
