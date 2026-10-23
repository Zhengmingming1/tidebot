package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func runIDs(list []map[string]any) string {
	ids := make([]string, len(list))
	for i, r := range list {
		ids[i] = fmt.Sprint(r["id"])
	}
	return strings.Join(ids, ",")
}

func runCounts(t *testing.T, c *client, q string) map[string]float64 {
	t.Helper()
	rec := c.do(http.MethodGet, "/api/runs/counts?"+q, "", nil)
	if rec.Code != 200 {
		t.Fatalf("counts %s: %d %s", q, rec.Code, rec.Body.String())
	}
	var out map[string]float64
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Filter groups: "failed" includes timeouts, "stopped" is canceled + skipped; the counts
// follow the same task and search text as the list.
func TestRunsFilterGroupsAndCounts(t *testing.T) {
	c := login(t)
	now := time.Now()
	for i, st := range []string{"succeeded", "succeeded", "failed", "timeout", "canceled", "skipped", "queued", "running"} {
		seedRun(t, c, fmt.Sprintf("g%d", i), "prompt "+st, st, 0.01, now.Add(time.Duration(i)*time.Minute))
	}
	if got := runIDs(listRuns(t, c, "status=failed")); got != "g3,g2" {
		t.Fatalf("failed = %s", got)
	}
	if got := runIDs(listRuns(t, c, "status=stopped")); got != "g5,g4" {
		t.Fatalf("stopped = %s", got)
	}
	if got := runIDs(listRuns(t, c, "status=running")); got != "g7,g6" {
		t.Fatalf("running = %s", got)
	}
	// an exact status still works
	if got := runIDs(listRuns(t, c, "status=timeout")); got != "g3" {
		t.Fatalf("timeout = %s", got)
	}
	want := map[string]float64{"all": 8, "running": 2, "succeeded": 2, "failed": 2, "stopped": 2}
	got := runCounts(t, c, "")
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("counts %v, want %v", got, want)
		}
	}
	// the status param is ignored by counts; search narrows them
	if got := runCounts(t, c, "status=failed&q=prompt+s"); got["all"] != 3 || got["succeeded"] != 2 || got["stopped"] != 1 || got["failed"] != 0 {
		t.Fatalf("search counts %v", got)
	}
	// injection attempts are plain text
	if got := runCounts(t, c, "q=x'+OR+1=1--"); got["all"] != 0 {
		t.Fatalf("injection counted %v", got)
	}
	// task filter
	code, out := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "daily", "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
	if code >= 300 {
		t.Fatalf("create task %d %v", code, out)
	}
	tid := fmt.Sprint(out["id"])
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET task_id=? WHERE id IN ('g2','g4')`, tid); err != nil {
		t.Fatal(err)
	}
	if got := runCounts(t, c, "task_id="+tid); got["all"] != 2 || got["failed"] != 1 || got["stopped"] != 1 {
		t.Fatalf("task counts %v", got)
	}
	if rec := c.do(http.MethodPost, "/api/runs/counts", "", nil); rec.Code != 405 {
		t.Fatalf("POST counts %d", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/api/runs/counts?q="+strings.Repeat("a", 101), "", nil); rec.Code != 400 {
		t.Fatalf("long q %d", rec.Code)
	}
}

// Paging: before=<last id> continues the time sorts without gaps or repeats, even across
// runs created in the same second and runs started between pages; offset pages the rest.
func TestRunsPaging(t *testing.T) {
	c := login(t)
	base := time.Now().Add(-time.Hour)
	var all []string
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("p%02d", i)
		at := base
		if i >= 3 {
			at = base.Add(time.Duration(i) * time.Minute)
		}
		seedRun(t, c, id, "x", "succeeded", float64(i)/100, at)
		all = append([]string{id}, all...)
	}
	var got []string
	q := "limit=3"
	for page := 0; page < 5; page++ {
		list := listRuns(t, c, q)
		if len(list) == 0 {
			break
		}
		for _, r := range list {
			got = append(got, fmt.Sprint(r["id"]))
		}
		if page == 0 {
			seedRun(t, c, "p99", "new", "running", 0, time.Now()) // a run starts meanwhile
		}
		q = "limit=3&before=" + fmt.Sprint(list[len(list)-1]["id"])
	}
	if strings.Join(got, ",") != strings.Join(all, ",") {
		t.Fatalf("new pages %v, want %v", got, all)
	}
	// oldest first
	first := listRuns(t, c, "sort=old&limit=2")
	if runIDs(first) != "p00,p01" {
		t.Fatalf("old page 1 %s", runIDs(first))
	}
	if got := runIDs(listRuns(t, c, "sort=old&limit=2&before=p01")); got != "p02,p03" {
		t.Fatalf("old page 2 %s", got)
	}
	// other sorts page by offset; before is refused there
	if got := runIDs(listRuns(t, c, "sort=cost&limit=2&offset=2")); got != "p04,p03" {
		t.Fatalf("cost offset %s", got)
	}
	if rec := c.do(http.MethodGet, "/api/runs?sort=cost&before=p01", "", nil); rec.Code != 400 {
		t.Fatalf("before+cost %d", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/api/runs?before=nope", "", nil); rec.Code != 400 {
		t.Fatalf("unknown before %d", rec.Code)
	}
	// with a filter the cursor still works
	if got := runIDs(listRuns(t, c, "status=succeeded&limit=2&before=p05")); got != "p04,p03" {
		t.Fatalf("filtered page %s", got)
	}
}

func TestRunsPageUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`["stopped", "runs.f_stopped", "stopped", "runs.f_stopped_tip"]`,
		`["failed", "st.failed", "failed", "runs.f_failed_tip"]`,
		`"/api/runs/counts?"`,
		`"&before=" + enc(list[list.length - 1].id) : "&offset=" + list.length`,
		`location.hash = runsHash(b.getAttribute("data-f"), task);`,
		`T("runs.shown_of", { n: num(shown.length), total: num(total) })`,
		`if (my !== v.seq || !$("#rtable")) return;`,
		`if (key !== v.key) { v.key = key; v.n = RUNS_PAGE; }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".runs-more {") {
		t.Error("styles.css lacks .runs-more")
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range []string{"runs.f_stopped", "runs.f_stopped_tip", "runs.f_failed_tip", "runs.more", "runs.shown_of", "runs.shown"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		if !strings.Contains(m["runs.shown_of"], "{total}") || !strings.Contains(m["runs.shown_of"], "{n}") {
			t.Errorf("%s: runs.shown_of placeholders", f)
		}
	}
}
