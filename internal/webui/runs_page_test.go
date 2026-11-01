package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// planOf returns EXPLAIN QUERY PLAN's detail lines joined with " | ".
func planOf(t *testing.T, c *client, q string, args ...any) string {
	t.Helper()
	rows, err := c.s.deps.DB.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var a, b, c2 int
		var d string
		if err := rows.Scan(&a, &b, &c2, &d); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, d)
	}
	return strings.Join(plan, " | ")
}

// The runs list pages its rows first and joins tasks / versions / skills onto the page:
// every filter, sort, search, offset and cursor gives exactly the rows the one-pass query
// it replaced gave, in the same order.
func TestRunsPageSameRows(t *testing.T) {
	c := login(t)
	tasks := seedHistory(t, c, 1200)
	ids := func(path string) []string {
		rec := c.do(http.MethodGet, path, "", nil)
		var list []struct {
			ID     string `json:"id"`
			Prompt string `json:"prompt"`
			Task   string `json:"task_name"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		out := make([]string, len(list))
		for i, r := range list {
			out[i] = r.ID
		}
		return out
	}
	ref := func(raw string) []string {
		q, _ := url.ParseQuery(raw)
		where, args, err := runsFilter(q, true, c.s.loc(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		order := c.s.runsOrder(q.Get("sort"))
		if b := q.Get("before"); b != "" {
			var created string
			if err := c.s.deps.DB.QueryRow(`SELECT created_at FROM runs WHERE id=?`, b).Scan(&created); err != nil {
				t.Fatal(err)
			}
			cmp := "<"
			if q.Get("sort") == "old" {
				cmp = ">"
			}
			where += " AND (r.created_at " + cmp + " ? OR (r.created_at = ? AND r.id " + cmp + " ?))"
			args = append(args, created, created, b)
		}
		lim, off := 50, 0
		if l, err := strconv.Atoi(q.Get("limit")); err == nil {
			lim = l
		}
		if o, err := strconv.Atoi(q.Get("offset")); err == nil {
			off = o
		}
		runs, err := c.s.queryRuns(context.Background(), where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, lim, off)...)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(runs))
		for i, r := range runs {
			out[i] = r.ID
		}
		return out
	}
	cases := []string{"", "sort=old", "sort=cost", "sort=tokens", "sort=duration", "status=failed", "status=succeeded",
		"status=stopped", "status=running", "status=failed&sort=duration", "range=7d", "range=30d&status=failed",
		"task_id=" + tasks[0], "task_id=" + tasks[1] + "&status=failed&sort=old", "q=%E6%97%A5%E6%8A%A5", "q=hr0012",
		"q=%E6%97%A5%E6%8A%A5&status=failed&sort=cost", "limit=7&offset=20", "sort=cost&limit=5&offset=40",
		"limit=1000", "q=nothing-matches"}
	first := ids("/api/runs?limit=60")
	cases = append(cases, "before="+first[59], "before="+first[10]+"&status=failed", "sort=old&before="+ref("sort=old")[30])
	for _, raw := range cases {
		got, want := ids("/api/runs?"+raw), ref(raw)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%q: got %d rows %v…, want %d %v…", raw, len(got), head(got), len(want), head(want))
		}
		if raw != "q=nothing-matches" && len(want) == 0 {
			t.Fatalf("%q: case matches nothing", raw)
		}
	}
	// search still fills in the task name and the hit excerpt
	rec := c.do(http.MethodGet, "/api/runs?q=%E6%97%A5%E6%8A%A5&limit=3", "", nil)
	if !strings.Contains(rec.Body.String(), `"task_name":"日报"`) {
		t.Fatalf("search lost the task name: %s", rec.Body.String())
	}
}

func head(s []string) []string {
	if len(s) > 3 {
		return s[:3]
	}
	return s
}

// The page query reads the list off an index: no sort of the whole runs table for the
// newest-first list, a status filter, a time range or a task.
func TestRunsPagePlan(t *testing.T) {
	c := login(t)
	seedHistory(t, c, 600)
	for _, tc := range []struct{ raw, index string }{
		{"", "idx_runs_created_id"},
		{"sort=old", "idx_runs_created_id"},
		{"status=succeeded", "idx_runs_status_created"},
		{"range=7d", "idx_runs_created_id"},
	} {
		q, _ := url.ParseQuery(tc.raw)
		where, args, err := runsFilter(q, true, c.s.loc(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		inner := "SELECT r.rowid FROM runs r " + where + " ORDER BY " + c.s.runsOrder(q.Get("sort")) + " LIMIT 50"
		p := planOf(t, c, inner, args...)
		if !strings.Contains(p, tc.index) || strings.Contains(p, "TEMP B-TREE") {
			t.Fatalf("%q: inner plan %s", tc.raw, p)
		}
		full := c.s.runSelect() + " " + runsPageWhere(false, where, c.s.runsOrder(q.Get("sort")), " LIMIT ?")
		if p := planOf(t, c, full, append(args, 50)...); !strings.Contains(p, "INTEGER PRIMARY KEY") || !strings.Contains(p, tc.index) {
			t.Fatalf("%q: page plan %s", tc.raw, p)
		}
	}
	// "failed" is two statuses: the page is sorted from the covering index alone
	q, _ := url.ParseQuery("status=failed")
	where, args, _ := runsFilter(q, true, c.s.loc(), time.Now())
	if p := planOf(t, c, "SELECT r.rowid FROM runs r "+where+" ORDER BY "+c.s.runsOrder("")+" LIMIT 50", args...); !strings.Contains(p, "COVERING INDEX idx_runs_status_created") {
		t.Fatalf("failed: %s", p)
	}
}

// The Run dialog's last-folder / recent-folders queries read only quick runs.
func TestRunDefaultsUseQuickIndex(t *testing.T) {
	c := login(t)
	seedHistory(t, c, 300)
	for _, q := range []string{
		`SELECT workspace_dir, run_mode, COALESCE(provider_name,'') FROM runs
WHERE trigger_kind='quick' AND workspace_dir<>'' AND run_mode<>'' ORDER BY created_at DESC LIMIT 1`,
		`SELECT workspace_dir, MAX(created_at) m, MAX(skill_id=?) k FROM runs
WHERE trigger_kind='quick' AND workspace_dir<>'' AND run_mode<>'' GROUP BY workspace_dir ORDER BY k DESC, m DESC LIMIT 12`,
	} {
		var args []any
		if strings.Contains(q, "?") {
			args = []any{""}
		}
		if p := planOf(t, c, q, args...); !strings.Contains(p, "idx_runs_quick_created") {
			t.Fatalf("plan %s", p)
		}
	}
	// and the handler still answers
	if code, out := c.json(http.MethodGet, "/api/runs/defaults", nil); code != 200 || out["recent_dirs"] == nil {
		t.Fatalf("defaults %d %v", code, out)
	}
}
