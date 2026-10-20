package webui

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSpendExportCSV(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	loc := c.s.deps.Cost.Location()
	now := time.Now()
	day := now.In(loc).Format("2006-01-02")
	old := now.AddDate(0, 0, -40)
	oldDay := old.In(loc).Format("2006-01-02")
	ts := now.UTC().Format(time.RFC3339)
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": `=HYPERLINK("http://x","日报")`, "prompt": "p", "provider": "anthropic"})
	tid := tk["id"].(string)
	db := c.s.deps.DB
	if _, err := db.ExecContext(ctx, `INSERT INTO runs(id,task_id,status,provider_name,model,created_at) VALUES('er1',?,'succeeded','kimi-cn','kimi-k3',?),('er2',NULL,'failed','anthropic','sonnet',?),('er3',NULL,'succeeded','anthropic','sonnet',?)`,
		tid, ts, ts, old.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cost_ledger(run_id,bucket,task_id,usd,tokens,input_tokens,output_tokens,cache_read_tokens,requests,amount,currency,source,provider,model,day,month,created_at) VALUES
('er1','production',?,0.25,1500,1000,500,200,3,0.25,'USD','priced','kimi-cn','kimi-k3',?,?,?),
('er2','production',NULL,0.0000123,10,8,2,0,1,0.0000123,'USD','estimate','','',?,?,?),
('er3','production',NULL,9,10,8,2,0,1,9,'USD','priced','','',?,?,?)`,
		tid, day, day[:7], ts, day, day[:7], ts, oldDay, oldDay[:7], old.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	rr := c.do(http.MethodGet, "/api/spend/export?range=7d&lang=zh-CN", "", nil)
	if rr.Code != 200 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "_to_"+day+".csv") || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %q %q", cd, rr.Header().Get("Cache-Control"))
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatal("no BOM")
	}
	recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || recs[0][0] != "日期" || len(recs[0]) != 16 {
		t.Fatalf("rows %v", recs)
	}
	var r1, r2 []string
	for _, r := range recs[1:] {
		switch r[3] {
		case "er1":
			r1 = r
		case "er2":
			r2 = r
		}
	}
	if r1 == nil || r2 == nil {
		t.Fatalf("missing runs %v", recs)
	}
	if r1[2] != `'=HYPERLINK("http://x","日报")` || r1[5] != "kimi-cn" || r1[6] != "kimi-k3" || r1[8] != "3" || r1[9] != "1000" ||
		r1[11] != "200" || r1[13] != "0.25" || r1[14] != "USD" || r1[15] != "按单价计算" {
		t.Fatalf("er1 %v", r1)
	}
	if r2[2] != "快速运行" || r2[5] != "anthropic" || r2[7] != "失败" || r2[13] != "0.000012" || r2[15] != "估算" {
		t.Fatalf("er2 %v", r2)
	}

	// all-time includes the 40-day-old run and names the file from its day; English header
	rr = c.do(http.MethodGet, "/api/spend/export?range=all&lang=en", "", nil)
	recs, _ = csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if len(recs) != 4 || recs[0][0] != "Date" || !strings.Contains(rr.Header().Get("Content-Disposition"), "tidebot-spend_"+oldDay+"_to_") {
		t.Fatalf("all %d %q", len(recs), rr.Header().Get("Content-Disposition"))
	}

	if code, _ := c.json(http.MethodGet, "/api/spend/export?range=1y", nil); code != 400 {
		t.Fatalf("bad range %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/spend/export", nil); code != 405 {
		t.Fatalf("post %d", code)
	}
	anon := httptest.NewRecorder()
	c.s.routes().ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/spend/export", nil))
	if anon.Code != 401 {
		t.Fatalf("anonymous %d", anon.Code)
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+x": "'+x", "-2": "'-2", "@sum": "'@sum", "\tx": "'\tx", "ok": "ok", "": "", "日报": "日报"} {
		if got := csvSafe(in); got != want {
			t.Fatalf("%q → %q want %q", in, got, want)
		}
	}
}
