package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// seedHistory makes four tasks (one archived, one paused) and n runs spread over them and
// over quick runs, inserted in random order with some equal created_at values (ties are
// broken by id), plus a ledger row per run with a few rows lacking provider / model.
func seedHistory(t *testing.T, c *client, n int) []string {
	t.Helper()
	ctx := context.Background()
	db := c.s.deps.DB
	tasks := []string{}
	for _, name := range []string{"日报", "weekly", "paused", "gone"} {
		code, out := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": name, "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
		if code >= 300 {
			t.Fatalf("create %s: %d %v", name, code, out)
		}
		tasks = append(tasks, fmt.Sprint(out["id"]))
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/"+tasks[2]+"/pause", map[string]any{}); code >= 300 {
		if _, err := db.Exec(`UPDATE tasks SET status='paused' WHERE id=?`, tasks[2]); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := c.json(http.MethodDelete, "/api/tasks/"+tasks[3], nil); code >= 300 {
		t.Fatalf("delete %d", code)
	}
	rnd := rand.New(rand.NewSource(7))
	sts := []string{"succeeded", "succeeded", "succeeded", "failed", "timeout", "canceled", "skipped", "running"}
	base := time.Now().Add(-40 * 24 * time.Hour).UTC()
	step := 40 * 24 * time.Hour / time.Duration(n/3+1) // up to about now
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range rnd.Perm(n) {
		var tid any
		if k := i % 5; k < 4 {
			tid = tasks[k]
		}
		at := base.Add(time.Duration(i/3) * step).Format(time.RFC3339) // 3 runs share each time
		id := fmt.Sprintf("hr%05d", i)
		cost := float64(rnd.Intn(500)) / 1000
		prov, model := []string{"anthropic", "kimi-cn", "deepseek"}[i%3], []string{"sonnet", "kimi-k3", "chat"}[i%3]
		if _, err := tx.Exec(`INSERT INTO runs(id,task_id,status,prompt,provider_name,model,total_cost_usd,duration_ms,created_at,started_at,finished_at,result_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, tid, sts[rnd.Intn(len(sts))], strings.Repeat("长提示词 ", 40), prov, model, cost, i*10, at, at, at, strings.Repeat("answer ", 200)); err != nil {
			t.Fatal(err)
		}
		lp, lm := prov, model
		if i%7 == 0 { // older ledger rows: provider / model only on the run
			lp, lm = "", ""
		}
		day := base.Add(time.Duration(i/3) * step).In(c.s.deps.Cost.Location()).Format("2006-01-02")
		src := []string{"priced", "estimate", "legacy", "subscription"}[i%4]
		if _, err := tx.Exec(`INSERT INTO cost_ledger(run_id,bucket,task_id,usd,tokens,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,requests,amount,currency,source,provider,model,day,month,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, "production", tid, cost, i, i/2, i/3, i%11, i%13, 1+i%3, cost, "USD", src, lp, lm, day, day[:7], at); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return tasks
}

// recentBeats (per-task index reads) returns exactly what the ROW_NUMBER() query it
// replaced returned, ties included.
func TestRecentBeatsMatchesWindow(t *testing.T) {
	c := login(t)
	seedHistory(t, c, 1500)
	ctx := context.Background()
	ref := func(where string, n int) map[string][]beat {
		rows, err := c.s.deps.DB.QueryContext(ctx, `SELECT task_id, id, status, created_at, COALESCE(duration_ms,0), `+c.s.runAmt("")+` FROM (
		SELECT r.*, ROW_NUMBER() OVER (PARTITION BY r.task_id ORDER BY r.created_at DESC, r.id DESC) AS rn
		FROM runs r JOIN tasks t ON t.id = r.task_id WHERE `+where+`) WHERE rn <= ? ORDER BY task_id, created_at ASC, id ASC`, n)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string][]beat{}
		for rows.Next() {
			var tid string
			var b beat
			if err := rows.Scan(&tid, &b.ID, &b.Status, &b.At, &b.DurationMS, &b.CostUSD); err != nil {
				t.Fatal(err)
			}
			out[tid] = append(out[tid], b)
		}
		return out
	}
	for _, tc := range []struct {
		where string
		n     int
	}{{"t.status <> 'archived'", taskListBeats}, {"t.status IN ('active','paused')", 20}, {"1=1", 1}, {"1=1", 400}} {
		got, err := c.s.recentBeats(ctx, tc.where, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		want := ref(tc.where, tc.n)
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s / %d: got %d tasks, want %d (differs)", tc.where, tc.n, len(got), len(want))
		}
	}
	// the plan reads runs through the (task_id, created_at) index, never the whole table
	rows, err := c.s.deps.DB.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT r.id FROM tasks t JOIN runs r ON r.rowid IN (SELECT rowid FROM runs WHERE task_id = t.id ORDER BY created_at DESC, id DESC LIMIT 10) WHERE t.status <> 'archived'`)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var a, b, c2 int
		var d string
		if err := rows.Scan(&a, &b, &c2, &d); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, d)
	}
	rows.Close()
	p := strings.Join(plan, " | ")
	if !strings.Contains(p, "idx_runs_task_created") || strings.Contains(p, "SCAN r ") || strings.HasSuffix(p, "SCAN r") || strings.Contains(p, "SCAN runs") {
		t.Fatalf("plan scans runs: %s", p)
	}
}

// Dashboard and task list keep their answers with a long history.
func TestTaskListAndDashboardHistory(t *testing.T) {
	c := login(t)
	tasks := seedHistory(t, c, 900)
	rec := c.do(http.MethodGet, "/api/tasks", "", nil)
	var list []struct {
		ID         string `json:"id"`
		LastStatus string `json:"last_status"`
		LastRunAt  string `json:"last_run_at"`
		Beats      []beat `json:"beats"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 3 {
		t.Fatalf("tasks %d %s", rec.Code, rec.Body.String())
	}
	for _, tk := range list {
		var id, st, at string
		if err := c.s.deps.DB.QueryRow(`SELECT id, status, created_at FROM runs WHERE task_id=? ORDER BY created_at DESC, id DESC LIMIT 1`, tk.ID).Scan(&id, &st, &at); err != nil {
			t.Fatal(err)
		}
		if len(tk.Beats) != taskListBeats || tk.Beats[len(tk.Beats)-1].ID != id || tk.LastStatus != st || tk.LastRunAt != at {
			t.Fatalf("task %s: %d beats, last %+v, want %s %s %s", tk.ID, len(tk.Beats), tk.Beats[len(tk.Beats)-1], id, st, at)
		}
		for i := 1; i < len(tk.Beats); i++ {
			if a, b := tk.Beats[i-1], tk.Beats[i]; a.At > b.At || (a.At == b.At && a.ID > b.ID) {
				t.Fatalf("beats not oldest first: %+v %+v", a, b)
			}
		}
	}
	_, dash := c.json(http.MethodGet, "/api/dashboard", nil)
	rows, _ := dash["tasks"].([]any)
	if len(rows) != 3 {
		t.Fatalf("dashboard tasks %v", dash["tasks"])
	}
	for _, x := range rows {
		m := x.(map[string]any)
		if bs, _ := m["beats"].([]any); len(bs) != 20 || m["id"] == tasks[3] {
			t.Fatalf("dashboard row %v: %d beats", m["id"], len(bs))
		}
	}
}

// Run counts without search text skip the tasks join but count the same; with it the
// task name still matches.
func TestRunCountsHistory(t *testing.T) {
	c := login(t)
	seedHistory(t, c, 600)
	count := func(q string) map[string]float64 {
		rec := c.do(http.MethodGet, "/api/runs/counts?"+q, "", nil)
		var m map[string]float64
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
			t.Fatalf("counts %s: %d %s", q, rec.Code, rec.Body.String())
		}
		return m
	}
	all := count("")
	var n, fail int
	_ = c.s.deps.DB.QueryRow(`SELECT COUNT(1), SUM(CASE WHEN status IN ('failed','timeout') THEN 1 ELSE 0 END) FROM runs`).Scan(&n, &fail)
	if int(all["all"]) != n || int(all["failed"]) != fail {
		t.Fatalf("counts %v, want all %d failed %d", all, n, fail)
	}
	byName := count("q=" + "%E6%97%A5%E6%8A%A5") // 日报: only in the task name
	if int(byName["all"]) != 120 {
		t.Fatalf("q=日报 counts %v, want 120", byName)
	}
}

// The spend breakdown sums first and joins afterwards: same rows as the old one-pass query.
func TestSpendBreakdownHistory(t *testing.T) {
	c := login(t)
	seedHistory(t, c, 1200)
	ctx := context.Background()
	type row struct {
		Key, Name                                          string
		Runs                                               int
		USD                                                float64
		Tokens, Input, Output, CacheRead, CacheWrite, Reqs int64
		Est, Leg, Sub                                      int
	}
	old := func(by, since string) []row {
		key := map[string]string{"task": "COALESCE(l.task_id,'')", "model": "COALESCE(NULLIF(l.model,''), r.model, '')",
			"provider_model": "COALESCE(NULLIF(l.provider,''), r.provider_name, '') || ' / ' || COALESCE(NULLIF(l.model,''), NULLIF(r.model,''), '—')"}[by]
		amt := c.s.ledgerAmt("l.")
		rows, err := c.s.deps.DB.QueryContext(ctx, `SELECT `+key+` AS k, COALESCE(MAX(t.name),''), COUNT(1), COALESCE(SUM(`+amt+`),0), COALESCE(SUM(l.tokens),0),
  COALESCE(SUM(l.input_tokens),0), COALESCE(SUM(l.output_tokens),0), COALESCE(SUM(l.cache_read_tokens),0), COALESCE(SUM(l.cache_write_tokens),0),
  COALESCE(SUM(l.requests),0), SUM(CASE WHEN l.source IN ('estimate','claude') THEN 1 ELSE 0 END),
  SUM(CASE WHEN l.source='legacy' THEN 1 ELSE 0 END), SUM(CASE WHEN l.source='subscription' THEN 1 ELSE 0 END)
FROM cost_ledger l LEFT JOIN runs r ON r.id = l.run_id LEFT JOIN tasks t ON t.id = l.task_id
WHERE l.day >= ? GROUP BY k ORDER BY SUM(`+amt+`) DESC, SUM(l.tokens) DESC LIMIT 50`, since)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.Key, &x.Name, &x.Runs, &x.USD, &x.Tokens, &x.Input, &x.Output, &x.CacheRead, &x.CacheWrite, &x.Reqs, &x.Est, &x.Leg, &x.Sub); err != nil {
				t.Fatal(err)
			}
			if by != "task" {
				x.Name = x.Key
			}
			out = append(out, x)
		}
		return out
	}
	for _, rg := range []string{"7d", "30d", "all"} {
		for _, by := range []string{"task", "model", "provider_model"} {
			var got struct {
				Rows []struct {
					Key        string  `json:"key"`
					Name       string  `json:"name"`
					Runs       int     `json:"runs"`
					USD        float64 `json:"usd"`
					Tokens     int64   `json:"tokens"`
					Input      int64   `json:"input_tokens"`
					Output     int64   `json:"output_tokens"`
					CacheRead  int64   `json:"cache_read_tokens"`
					CacheWrite int64   `json:"cache_write_tokens"`
					Requests   int64   `json:"requests"`
					Estimated  int     `json:"estimated_runs"`
					Legacy     int     `json:"legacy_runs"`
					Sub        int     `json:"subscription_runs"`
				} `json:"rows"`
				Since string `json:"since"`
				Days  []struct {
					Day string `json:"day"`
				} `json:"days"`
			}
			rec := c.do(http.MethodGet, "/api/spend/breakdown?range="+rg+"&by="+by, "", nil)
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
				t.Fatalf("%s %s: %d %s", rg, by, rec.Code, rec.Body.String())
			}
			want := old(by, got.Since)
			if len(want) == 0 || len(got.Rows) != len(want) {
				t.Fatalf("%s %s: %d rows, want %d", rg, by, len(got.Rows), len(want))
			}
			for i, g := range got.Rows {
				w := want[i]
				if g.Key != w.Key || g.Name != w.Name || g.Runs != w.Runs || fmt.Sprintf("%.6f", g.USD) != fmt.Sprintf("%.6f", w.USD) || g.Tokens != w.Tokens ||
					g.Input != w.Input || g.Output != w.Output || g.CacheRead != w.CacheRead || g.CacheWrite != w.CacheWrite || g.Requests != w.Reqs ||
					g.Estimated != w.Est || g.Legacy != w.Leg || g.Sub != w.Sub {
					t.Fatalf("%s %s row %d: got %+v, want %+v", rg, by, i, g, w)
				}
			}
			// the chart series never reaches back past 90 days
			cut := time.Now().In(c.s.deps.Cost.Location()).AddDate(0, 0, -89).Format("2006-01-02")
			if len(got.Days) == 0 || got.Days[0].Day < cut || got.Days[0].Day < got.Since {
				t.Fatalf("%s %s: days start %v", rg, by, got.Days)
			}
		}
	}
}
