package webui

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpendDay(t *testing.T) {
	for _, c := range []struct {
		in, since, want string
		bad             bool
	}{
		{"", "2026-10-18", "", false},
		{"2026-10-20", "2026-10-18", "2026-10-20", false},
		{"2026-10-18", "2026-10-18", "2026-10-18", false},
		{"2026-10-17", "2026-10-18", "", true}, // before the range
		{"2026-02-30", "0000-00-00", "", true}, // not a real date
		{"2026-1-5", "0000-00-00", "", true},
		{"2026-10-20' OR 1=1--", "0000-00-00", "", true},
	} {
		got, err := spendDay(c.in, c.since)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("spendDay(%q,%q) = %q, %v", c.in, c.since, got, err)
		}
	}
}

func TestSpendBreakdownDay(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	loc := c.s.deps.Cost.Location()
	now := time.Now()
	today := now.In(loc).Format("2006-01-02")
	prev := now.In(loc).AddDate(0, 0, -2)
	prevDay := prev.Format("2006-01-02")
	ts, pts := now.UTC().Format(time.RFC3339), prev.UTC().Format(time.RFC3339)
	db := c.s.deps.DB
	if _, err := db.ExecContext(ctx, `INSERT INTO runs(id,task_id,status,provider_name,model,created_at) VALUES('sd1',NULL,'succeeded','kimi-cn','kimi-k3',?),('sd2',NULL,'succeeded','anthropic','sonnet',?),('sd3',NULL,'succeeded','anthropic','sonnet',?)`, ts, pts, pts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cost_ledger(run_id,bucket,task_id,usd,tokens,amount,currency,source,provider,model,day,month,created_at) VALUES
('sd1','production',NULL,0.5,100,0.5,'USD','priced','kimi-cn','kimi-k3',?,?,?),
('sd2','production',NULL,2,200,2,'USD','priced','anthropic','sonnet',?,?,?),
('sd3','production',NULL,1,300,1,'USD','priced','anthropic','sonnet',?,?,?)`,
		today, today[:7], ts, prevDay, prevDay[:7], pts, prevDay, prevDay[:7], pts); err != nil {
		t.Fatal(err)
	}
	type bk struct {
		Rows []struct {
			Key  string  `json:"key"`
			Runs int     `json:"runs"`
			USD  float64 `json:"usd"`
		} `json:"rows"`
		Days  []struct{ Day string } `json:"days"`
		Total float64                `json:"total_usd"`
		Day   string                 `json:"day"`
	}
	get := func(q string) (int, bk) {
		var v bk
		rr := c.do(http.MethodGet, "/api/spend/breakdown?"+q, "", nil)
		if rr.Code == 200 {
			if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
		}
		return rr.Code, v
	}
	code, all := get("range=7d&by=model")
	if code != 200 || all.Total != 3.5 || len(all.Rows) != 2 || all.Day != "" {
		t.Fatalf("whole range %d %+v", code, all)
	}
	code, one := get("range=7d&by=model&day=" + prevDay)
	if code != 200 || one.Total != 3 || len(one.Rows) != 1 || one.Rows[0].Key != "sonnet" || one.Rows[0].Runs != 2 || one.Day != prevDay {
		t.Fatalf("one day %d %+v", code, one)
	}
	// the chart series still covers the whole range
	if len(one.Days) != len(all.Days) || len(one.Days) != 2 {
		t.Fatalf("series %+v vs %+v", one.Days, all.Days)
	}
	if _, empty := get("range=30d&by=task&day=" + now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")); empty.Total != 0 || len(empty.Rows) != 0 {
		t.Fatalf("empty day %+v", empty)
	}
	for _, q := range []string{"day=2026-13-01", "day=x", "range=7d&day=" + now.In(loc).AddDate(0, 0, -9).Format("2006-01-02")} {
		if code, _ := get(q); code != 400 {
			t.Errorf("%s → %d, want 400", q, code)
		}
	}

	// export follows the picked day and names the file after it
	rr := c.do(http.MethodGet, "/api/spend/export?range=7d&day="+prevDay+"&lang=en", "", nil)
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Disposition"), "tidebot-spend_"+prevDay+".csv") {
		t.Fatalf("export %d %q", rr.Code, rr.Header().Get("Content-Disposition"))
	}
	recs, _ := csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if len(recs) != 3 || recs[1][0] != prevDay || recs[2][0] != prevDay {
		t.Fatalf("export rows %v", recs)
	}
	if rr := c.do(http.MethodGet, "/api/spend/export?range=7d&day=bad", "", nil); rr.Code != 400 {
		t.Fatalf("bad export day %d", rr.Code)
	}
}

func TestSpendDayUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function spendChart(days, range, sel) {`,
		`'<button type="button" class="spc-col'`,
		`aria-pressed="' + on + '" tabindex="' + (k === stop ? 0 : -1) + '"`,
		`function spendHash(v) {`,
		`case "spend": p = renderSpend(params); break;`,
		`{ ArrowLeft: i - 1, ArrowRight: i + 1, Home: 0, End: all.length - 1 }[e.key]`,
		`if (e.key === "Escape" && spendView.day)`,
		`href="#/runs?from=' + enc(dy) + "&to=" + enc(dy) + '"`,
		`(spendView.day ? "&day=" + enc(spendView.day) : "")`,
		`if (spendView.day && !spendDayOk(spendView.range, spendView.day)) spendView.day = "";`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	i := strings.Index(src, "function spendChartKeys(")
	j := strings.Index(src, "// ---------- SETTINGS ----------")
	if i < 0 || j < i {
		t.Fatal("spend block not found")
	}
	for _, bad := range []string{"window.confirm(", "alert(", "prompt("} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("spend page uses %s", bad)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, want := range []string{"button.spc-col {", ".spc-plot.has-sel .spc-col .spc-bar {", ".sp-day {", "button.spc-col:focus-visible .spc-bar {"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	keys := []string{"sp.day_pick", "sp.day_unpick", "sp.day_only", "sp.day_clear", "sp.day_runs", "sp.none_day", "sp.export_day_tip"}
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
