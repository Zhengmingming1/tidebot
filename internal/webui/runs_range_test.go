package webui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Ranges are whole calendar days in the scheduler's zone; from/to include both days.
func TestRunsRange(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600) // fixed zone: no tzdata needed on Windows CI
	now := time.Date(2026, 10, 24, 0, 30, 0, 0, loc)
	day := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, loc) }
	cases := []struct {
		q        string
		from, to time.Time
		ok       bool
		bad      bool
	}{
		{q: ""},
		{q: "range=all"},
		{q: "range=today", from: day(2026, 10, 24), to: day(2026, 10, 25), ok: true},
		{q: "range=yesterday", from: day(2026, 10, 23), to: day(2026, 10, 24), ok: true},
		{q: "range=7d", from: day(2026, 10, 18), to: day(2026, 10, 25), ok: true},
		{q: "range=30d", from: day(2026, 9, 25), to: day(2026, 10, 25), ok: true},
		{q: "from=2026-10-01&to=2026-10-03", from: day(2026, 10, 1), to: day(2026, 10, 4), ok: true},
		{q: "from=2026-10-05&to=2026-10-05", from: day(2026, 10, 5), to: day(2026, 10, 6), ok: true},
		{q: "from=2026-10-20", from: day(2026, 10, 20), to: day(2026, 10, 25), ok: true},
		{q: "to=2026-10-02", from: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), to: day(2026, 10, 3), ok: true},
		{q: "range=week", bad: true},
		{q: "from=2026-13-01", bad: true},
		{q: "from=10/01/2026", bad: true},
		{q: "to=2026-10-02'%20OR%201=1", bad: true},
		{q: "from=2026-10-05&to=2026-10-04", bad: true},
		{q: "from=2000-01-01&to=2026-10-04", from: day(2000, 1, 1), to: day(2026, 10, 5), ok: true},
	}
	for _, c := range cases {
		v, _ := url.ParseQuery(c.q)
		from, to, ok, err := runsRange(v, loc, now)
		if c.bad {
			if err == nil {
				t.Errorf("%s: want error", c.q)
			}
			continue
		}
		if err != nil || ok != c.ok || (ok && (!from.Equal(c.from) || !to.Equal(c.to))) {
			t.Errorf("%s: got %v %v %v %v, want %v %v %v", c.q, from, to, ok, err, c.from, c.to, c.ok)
		}
	}
	// the same instant late in the UTC day is already "tomorrow" in UTC+8
	late := time.Date(2026, 10, 24, 17, 0, 0, 0, time.UTC) // 01:00 on the 25th in UTC+8
	from, _, _, _ := runsRange(url.Values{"range": {"today"}}, loc, late)
	if !from.Equal(day(2026, 10, 25)) {
		t.Fatalf("today in UTC+8 at %v = %v", late, from)
	}
}

// The list and its counts follow the range; paging and search combine with it.
func TestRunsDateRangeFilter(t *testing.T) {
	c := login(t)
	n := time.Now().In(time.Local)
	day0 := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
	at := func(days int, h int) time.Time { return day0.AddDate(0, 0, days).Add(time.Duration(h) * time.Hour) }
	seedRun(t, c, "d0a", "alpha", "succeeded", 0.01, at(0, 1))
	seedRun(t, c, "d0b", "beta", "failed", 0.01, at(0, 2))
	seedRun(t, c, "d1", "alpha", "succeeded", 0.01, at(-1, 3))
	seedRun(t, c, "d3", "alpha", "timeout", 0.01, at(-3, 4))
	seedRun(t, c, "d40", "alpha", "succeeded", 0.01, at(-40, 5))
	check := func(q, want string) {
		t.Helper()
		if got := runIDs(listRuns(t, c, q)); got != want {
			t.Fatalf("%s = %s, want %s", q, got, want)
		}
	}
	check("range=today", "d0b,d0a")
	check("range=yesterday", "d1")
	check("range=7d", "d0b,d0a,d1,d3")
	check("range=30d&status=failed", "d0b,d3")
	check("range=7d&q=alpha", "d0a,d1,d3")
	check("", "d0b,d0a,d1,d3,d40")
	d := func(days int) string { return day0.AddDate(0, 0, days).Format("2006-01-02") }
	check("from="+d(-3)+"&to="+d(-1), "d1,d3")
	check("to="+d(-3), "d3,d40")
	check("from="+d(-1), "d0b,d0a,d1")
	// keyset paging stays inside the range
	check("range=7d&limit=2", "d0b,d0a")
	check("range=7d&limit=2&before=d0a", "d1,d3")
	check("range=7d&sort=old&limit=2&before=d1", "d0a,d0b")
	got := runCounts(t, c, "range=7d")
	if got["all"] != 4 || got["failed"] != 2 || got["succeeded"] != 2 {
		t.Fatalf("7d counts %v", got)
	}
	if got := runCounts(t, c, "range=today&status=failed"); got["all"] != 2 || got["failed"] != 1 {
		t.Fatalf("today counts %v", got)
	}
	// the home page "today" count uses the same day boundary as range=today
	_, dash := c.json(http.MethodGet, "/api/dashboard", nil)
	if k, _ := dash["kpi"].(map[string]any); k == nil || k["runs_today"].(float64) != 2 {
		t.Fatalf("dashboard kpi %v", dash["kpi"])
	}
	for _, q := range []string{"range=week", "from=2026-02-30", "from=" + d(0) + "&to=" + d(-1), "to=x"} {
		if rec := c.do(http.MethodGet, "/api/runs?"+q, "", nil); rec.Code != 400 {
			t.Errorf("runs %s: %d", q, rec.Code)
		}
		if rec := c.do(http.MethodGet, "/api/runs/counts?"+q, "", nil); rec.Code != 400 {
			t.Errorf("counts %s: %d", q, rec.Code)
		}
	}
}

func TestRunsDateRangeUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`var RUN_RANGES = ["", "today", "yesterday", "7d", "30d"];`,
		`function rangePicker(btn, rg, onPick) {`,
		`rg = runsRangeOf(params);`,
		`runsQuery(st, task, v, rg) + runsMoreNext`,
		`runsHash(b.getAttribute("data-f"), task, rg)`,
		`rangePicker(e.currentTarget, rg, setRange)`,
		`href="#/runs?range=today"`,
		`go("#/runs?range=today")`,
		`T("rr.empty", { r: rangeLabel(rg) })`,
		`{ ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, bad := range []string{"window.confirm(", "alert(", "prompt("} {
		if i := strings.Index(src, "function rangePicker("); i >= 0 && strings.Contains(src[i:i+8000], bad) {
			t.Errorf("rangePicker uses %s", bad)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, want := range []string{".popover.rr-pop {", ".rr-d.start, .rr-d.end {", "a.card.stat.stat-link {"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	keys := []string{"rr.all", "rr.today", "rr.yesterday", "rr.7d", "rr.30d", "rr.since", "rr.until", "rr.tip", "rr.tip_all", "rr.title", "rr.quick",
		"rr.prev", "rr.next", "rr.pick_start", "rr.pick_end", "rr.clear", "rr.apply", "rr.empty", "rr.empty_sub", "rr.show_all", "pal.runs_today", "home.k_runs_tip"}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
