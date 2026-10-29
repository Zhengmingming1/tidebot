package webui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpendBreakdownByTag(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	mk := func(name string, tags []string) string {
		code, v := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": name, "prompt": "p", "provider": "anthropic", "tags": tags})
		if code != 201 {
			t.Fatalf("create %s %d %v", name, code, v)
		}
		return v["id"].(string)
	}
	a := mk("日报", []string{"日报", "Daily"})
	b := mk("监控", []string{"daily"}) // joins "Daily" (same tag, other case)
	n := mk("杂项", nil)
	loc := c.s.deps.Cost.Location()
	now := time.Now()
	today := now.In(loc).Format("2006-01-02")
	ts := now.UTC().Format(time.RFC3339)
	db := c.s.deps.DB
	for i, r := range []struct {
		task any
		usd  float64
	}{{a, 1}, {a, 2}, {b, 4}, {n, 8}, {nil, 16}} {
		id := "tg" + string(rune('a'+i))
		if _, err := db.ExecContext(ctx, `INSERT INTO runs(id,task_id,status,provider_name,model,created_at) VALUES(?,?,'succeeded','anthropic','sonnet',?)`, id, r.task, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO cost_ledger(run_id,bucket,task_id,usd,tokens,amount,currency,source,provider,model,day,month,created_at) VALUES(?,'production',?,?,10,?,'USD','priced','anthropic','sonnet',?,?,?)`,
			id, r.task, r.usd, r.usd, today, today[:7], ts); err != nil {
			t.Fatal(err)
		}
	}
	type row struct {
		Key   string  `json:"key"`
		Runs  int     `json:"runs"`
		USD   float64 `json:"usd"`
		Tasks int     `json:"tasks"`
	}
	var v struct {
		Rows    []row   `json:"rows"`
		Total   float64 `json:"total_usd"`
		Overlap bool    `json:"overlap"`
	}
	rr := c.do(http.MethodGet, "/api/spend/breakdown?range=7d&by=tag", "", nil)
	if rr.Code != 200 {
		t.Fatalf("by tag %d %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	// the total is the range's spend, not the (overlapping) sum of the rows
	if math.Abs(v.Total-31) > 1e-9 || !v.Overlap {
		t.Fatalf("total %v overlap %v", v.Total, v.Overlap)
	}
	got := map[string]row{}
	for _, x := range v.Rows {
		got[strings.ToLower(x.Key)] = x
	}
	if len(v.Rows) != 3 {
		t.Fatalf("rows %+v", v.Rows)
	}
	if x := got["daily"]; x.USD != 7 || x.Runs != 3 || x.Tasks != 2 {
		t.Fatalf("daily %+v", x)
	}
	if x := got["日报"]; x.USD != 3 || x.Runs != 2 || x.Tasks != 1 {
		t.Fatalf("日报 %+v", x)
	}
	// untagged task and the quick run share the '' row
	if x := got[""]; x.USD != 24 || x.Runs != 2 || x.Tasks != 1 {
		t.Fatalf("untagged %+v", x)
	}
	if v.Rows[0].Key != "" {
		t.Fatalf("order %+v", v.Rows)
	}
	// other tabs don't overlap
	rr = c.do(http.MethodGet, "/api/spend/breakdown?range=7d&by=task", "", nil)
	var w struct {
		Total   float64 `json:"total_usd"`
		Overlap bool    `json:"overlap"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &w)
	if math.Abs(w.Total-31) > 1e-9 || w.Overlap {
		t.Fatalf("by task %+v", w)
	}
	if rr := c.do(http.MethodGet, "/api/spend/breakdown?by=tags", "", nil); rr.Code != 400 {
		t.Fatalf("bad by %d", rr.Code)
	}
}

func TestSpendByTagUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`SPEND_BYS = ["task", "provider_model", "model", "provider", "tag"];`,
		`.concat(hasTags ? [["tag", T("sp.by_tag")]] : [])`,
		`href="#/tasks?tag=' + enc(x.key) + '"`,
		`spendView.by === "tag" && r.overlap ? tip(T("sp.tag_overlap")) : ""`,
		`"<tr><td>" + nameCell(x) + badge + "</td>`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	keys := []string{"sp.by_tag", "sp.col_tag", "sp.no_tag", "sp.no_tag_tip", "sp.tag_tip_one", "sp.tag_tip_many", "sp.tag_overlap"}
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
