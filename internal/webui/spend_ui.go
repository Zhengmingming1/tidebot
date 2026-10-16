package webui

import (
	"net/http"
	"time"
)

// GET /api/spend/breakdown?range=7d|30d|all&by=task|model|provider
func (s *Server) handleSpendBreakdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	q := r.URL.Query()
	days := map[string]int{"": 7, "7d": 7, "30d": 30, "all": 0}
	n, ok := days[q.Get("range")]
	if !ok {
		writeErr(w, 400, "range must be 7d, 30d or all")
		return
	}
	keyExpr := map[string]string{
		"":         "COALESCE(l.task_id,'')",
		"task":     "COALESCE(l.task_id,'')",
		"model":    "COALESCE(r.model,'')",
		"provider": "COALESCE(r.provider_name,'')",
	}[q.Get("by")]
	if keyExpr == "" {
		writeErr(w, 400, "by must be task, model or provider")
		return
	}
	loc := s.deps.Cost.Location()
	since := "0000-00-00"
	if n > 0 {
		since = time.Now().In(loc).AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	}
	type row struct {
		Key    string  `json:"key"`
		Name   string  `json:"name"`
		Runs   int     `json:"runs"`
		USD    float64 `json:"usd"`
		Tokens int64   `json:"tokens"`
	}
	ctx := r.Context()
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT `+keyExpr+` AS k, COALESCE(MAX(t.name),''), COUNT(1), COALESCE(SUM(l.usd),0), COALESCE(SUM(l.tokens),0)
FROM cost_ledger l LEFT JOIN runs r ON r.id = l.run_id LEFT JOIN tasks t ON t.id = l.task_id
WHERE l.day >= ? GROUP BY k ORDER BY SUM(l.usd) DESC LIMIT 50`, since)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	out := []row{}
	var total float64
	for rows.Next() {
		var x row
		if rows.Scan(&x.Key, &x.Name, &x.Runs, &x.USD, &x.Tokens) == nil {
			if q.Get("by") != "task" && q.Get("by") != "" {
				x.Name = x.Key
			}
			total += x.USD
			out = append(out, x)
		}
	}
	rows.Close()
	type day struct {
		Day string  `json:"day"`
		USD float64 `json:"usd"`
	}
	series := []day{}
	drows, err := s.deps.DB.QueryContext(ctx, `SELECT day, COALESCE(SUM(usd),0) FROM cost_ledger WHERE day >= ? GROUP BY day ORDER BY day DESC LIMIT 90`, since)
	if err == nil {
		for drows.Next() {
			var d day
			if drows.Scan(&d.Day, &d.USD) == nil {
				series = append([]day{d}, series...)
			}
		}
		drows.Close()
	}
	writeJSON(w, 200, map[string]any{"rows": out, "days": series, "total_usd": total, "since": since})
}
