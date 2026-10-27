package webui

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRunsExport: the runs list's Export takes the same filters and sort as the list and
// returns every matching run — CSV (BOM, localized, formula-safe, local times) or JSONL
// (English keys, UTC); bad input is refused.
func TestRunsExport(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": `=cmd|' /C calc'!A0`, "prompt": "p", "provider": "anthropic"})
	tid := tk["id"].(string)
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	db := c.s.deps.DB
	if _, err := db.ExecContext(ctx, `INSERT INTO runs(id,task_id,status,trigger_kind,provider_name,model,prompt,error_message,duration_ms,input_tokens,output_tokens,created_at,started_at,finished_at) VALUES
('xr1',?,'succeeded','cron','kimi-cn','kimi-k3','日报 prompt','',12345,1000,500,?,?,?),
('xr2',?,'failed','manual','anthropic','sonnet','second','boom
line two',800,10,2,?,?,?),
('xr3',NULL,'timeout','quick','anthropic','sonnet','`+strings.Repeat("长", 3000)+`','',0,0,0,?,'',''),
('xr4',NULL,'succeeded','quick','anthropic','sonnet','old one','',0,0,0,?,'','')`,
		tid, ts(-3*time.Minute), ts(-3*time.Minute), ts(-2*time.Minute),
		tid, ts(-2*time.Minute), ts(-2*time.Minute), ts(-time.Minute),
		ts(-time.Minute), now.AddDate(0, 0, -40).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	read := func(path string) [][]string {
		t.Helper()
		rr := c.do(http.MethodGet, path, "", nil)
		if rr.Code != 200 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/csv") {
			t.Fatalf("%s: %d %s %s", path, rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
		}
		if rr.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rr.Header().Get("Content-Disposition"), `attachment; filename="tidebot-runs_`) {
			t.Fatalf("headers %v", rr.Header())
		}
		body := rr.Body.String()
		if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
			t.Fatal("no BOM")
		}
		recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xEF\xBB\xBF"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		return recs
	}

	// all runs, newest first, Chinese
	recs := read("/api/runs/export?lang=zh-CN")
	if len(recs) != 5 || recs[0][0] != "日期" || len(recs[0]) != 20 {
		t.Fatalf("all: %d rows, head %v", len(recs), recs[0])
	}
	if recs[1][4] != "xr3" || recs[4][4] != "xr4" {
		t.Fatalf("order %v %v", recs[1][4], recs[4][4])
	}
	byID := map[string][]string{}
	for _, r := range recs[1:] {
		byID[r[4]] = r
	}
	r1, r2, r3 := byID["xr1"], byID["xr2"], byID["xr3"]
	if !strings.HasPrefix(r1[3], "'=") || r1[5] != "成功" || r1[6] != "定时" || r1[10] != "12.3" || r1[12] != "1000" || r1[13] != "500" {
		t.Fatalf("xr1 %v", r1)
	}
	if want := now.Add(-3 * time.Minute).In(c.s.loc()).Format("15:04:05"); r1[1] != want || r1[2] == "" {
		t.Fatalf("xr1 times %q %q want %q", r1[1], r1[2], want)
	}
	if r2[5] != "失败" || r2[6] != "手动" || r2[18] != "boom line two" {
		t.Fatalf("xr2 %v", r2)
	}
	if r3[3] != "快速运行" || r3[5] != "超时" || r3[2] != "" || r3[10] != "" || !strings.HasSuffix(r3[19], "…") || len(r3[19]) > exportPromptMax+len("…") {
		t.Fatalf("xr3 len %d %v", len(r3[19]), r3[:12])
	}

	// the list's filters: failed chip (failed + timeout), today, search, task, sort
	if recs := read("/api/runs/export?status=failed&range=7d"); len(recs) != 3 || recs[0][0] != "Date" {
		t.Fatalf("failed 7d %d", len(recs))
	}
	if recs := read("/api/runs/export?q=" + "second"); len(recs) != 2 || recs[1][4] != "xr2" {
		t.Fatalf("search %v", recs)
	}
	if recs := read("/api/runs/export?task_id=" + tid + "&sort=duration"); len(recs) != 3 || recs[1][4] != "xr1" {
		t.Fatalf("task/sort %v", recs)
	}
	if recs := read("/api/runs/export?sort=old"); recs[1][4] != "xr4" {
		t.Fatalf("old first %v", recs[1][4])
	}

	// file name follows the time range
	day := now.In(c.s.loc()).Format("2006-01-02")
	rr := c.do(http.MethodGet, "/api/runs/export?range=today", "", nil)
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, `tidebot-runs_`+day+`.csv"`) {
		t.Fatalf("today name %q", cd)
	}
	rr = c.do(http.MethodGet, "/api/runs/export", "", nil)
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, `tidebot-runs_all_to_`+day+`.csv"`) {
		t.Fatalf("all name %q", cd)
	}

	// JSONL
	rr = c.do(http.MethodGet, "/api/runs/export?format=jsonl&status=failed", "", nil)
	if rr.Code != 200 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "application/x-ndjson") || !strings.Contains(rr.Header().Get("Content-Disposition"), ".jsonl") {
		t.Fatalf("jsonl %d %v", rr.Code, rr.Header())
	}
	var lines []exportRun
	sc := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e exportRun
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		lines = append(lines, e)
	}
	if len(lines) != 2 || lines[0].ID != "xr3" || lines[1].ID != "xr2" || lines[1].Error != "boom\nline two" || lines[1].Trigger != "manual" || lines[1].Status != "failed" || lines[1].CreatedAt != ts(-2*time.Minute) {
		t.Fatalf("jsonl %+v", lines)
	}

	// refused
	for _, p := range []string{"/api/runs/export?format=xlsx", "/api/runs/export?sort=name", "/api/runs/export?range=year", "/api/runs/export?q=" + strings.Repeat("x", 101)} {
		if rr := c.do(http.MethodGet, p, "", nil); rr.Code != 400 {
			t.Errorf("%s: %d", p, rr.Code)
		}
	}
	if rr := c.do(http.MethodPost, "/api/runs/export", "{}", nil); rr.Code != 405 && rr.Code != 403 {
		t.Errorf("POST: %d", rr.Code)
	}
}

func TestClip(t *testing.T) {
	if clip("abc", 5) != "abc" || clip("abcdef", 3) != "abc…" {
		t.Fatal("ascii")
	}
	if got := clip("长长长", 4); got != "长…" {
		t.Fatalf("rune boundary %q", got)
	}
}

// TestRunsExportUI: the runs page has an Export menu that sends the list's own query and
// offers CSV and JSONL; no native dialogs, both languages.
func TestRunsExportUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`id="r-export" aria-haspopup="menu"`,
		`function runsExportMenu(anchor, o) {`,
		`"/api/runs/export?format=" + fmt + "&lang=" + enc(state.lang) + "&" + runsQuery(o.st, o.task, o.v, o.rg)`,
		`item("csv", "list") + item("jsonl", "code")`,
		`function popMenu(anchor, dir, o) {`,
		`id: "spz-menu", label: o.title,`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	i, j := strings.Index(src, "function runsExportMenu("), strings.Index(src, "// ---------- LogView: categories")
	if i < 0 || j < i {
		t.Fatal("export block not found")
	}
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "+ o.v.q +", "+ o.taskName +"} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("export block uses %s", bad)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"rx.export", "rx.tip", "rx.title_n", "rx.title", "rx.f_q", "rx.csv", "rx.csv_sub", "rx.jsonl", "rx.jsonl_sub", "rx.note", "rx.cap"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".rx-menu { width: 304px; }") {
		t.Error("styles lack .rx-menu")
	}
}
