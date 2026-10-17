package webui

import (
	"net/http"
	"time"
)

// GET /api/spend/breakdown?range=7d|30d|all&by=task|model|provider
// Amounts are in the display currency (fixed user-set rate). Rows carry the
// token breakdown and how many runs were estimates (missing / approximate
// prices, legacy Claude figures) so the UI can show an 估算 badge.
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
	by := q.Get("by")
	keyExpr := map[string]string{
		"":         "COALESCE(l.task_id,'')",
		"task":     "COALESCE(l.task_id,'')",
		"model":    "COALESCE(NULLIF(l.model,''), r.model, '')",
		"provider": "COALESCE(NULLIF(l.provider,''), r.provider_name, '')",
		// provider + model, separated by a slash
		"provider_model": "COALESCE(NULLIF(l.provider,''), r.provider_name, '') || ' / ' || COALESCE(NULLIF(l.model,''), NULLIF(r.model,''), '—')",
	}[by]
	if keyExpr == "" {
		writeErr(w, 400, "by must be task, model, provider or provider_model")
		return
	}
	loc := s.deps.Cost.Location()
	since := "0000-00-00"
	if n > 0 {
		since = time.Now().In(loc).AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	}
	type row struct {
		Key        string  `json:"key"`
		Name       string  `json:"name"`
		Runs       int     `json:"runs"`
		USD        float64 `json:"usd"` // display currency
		Tokens     int64   `json:"tokens"`
		Input      int64   `json:"input_tokens"`
		Output     int64   `json:"output_tokens"`
		CacheRead  int64   `json:"cache_read_tokens"`
		CacheWrite int64   `json:"cache_write_tokens"`
		Requests   int64   `json:"requests"`
		Estimated  int     `json:"estimated_runs"`
		Legacy     int     `json:"legacy_runs"`
		Sub        int     `json:"subscription_runs"`
	}
	amt := s.ledgerAmt("l.")
	ctx := r.Context()
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT `+keyExpr+` AS k, COALESCE(MAX(t.name),''), COUNT(1), COALESCE(SUM(`+amt+`),0), COALESCE(SUM(l.tokens),0),
  COALESCE(SUM(l.input_tokens),0), COALESCE(SUM(l.output_tokens),0), COALESCE(SUM(l.cache_read_tokens),0), COALESCE(SUM(l.cache_write_tokens),0),
  COALESCE(SUM(l.requests),0), SUM(CASE WHEN l.source IN ('estimate','claude') THEN 1 ELSE 0 END),
  SUM(CASE WHEN l.source='legacy' THEN 1 ELSE 0 END), SUM(CASE WHEN l.source='subscription' THEN 1 ELSE 0 END)
FROM cost_ledger l LEFT JOIN runs r ON r.id = l.run_id LEFT JOIN tasks t ON t.id = l.task_id
WHERE l.day >= ? GROUP BY k ORDER BY SUM(`+amt+`) DESC, SUM(l.tokens) DESC LIMIT 50`, since)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	out := []row{}
	var total float64
	var estimated int
	for rows.Next() {
		var x row
		if rows.Scan(&x.Key, &x.Name, &x.Runs, &x.USD, &x.Tokens, &x.Input, &x.Output, &x.CacheRead, &x.CacheWrite,
			&x.Requests, &x.Estimated, &x.Legacy, &x.Sub) == nil {
			if by != "task" && by != "" {
				x.Name = x.Key
			}
			total += x.USD
			estimated += x.Estimated + x.Legacy
			out = append(out, x)
		}
	}
	rows.Close()
	type day struct {
		Day string  `json:"day"`
		USD float64 `json:"usd"`
	}
	series := []day{}
	drows, err := s.deps.DB.QueryContext(ctx, `SELECT day, COALESCE(SUM(`+s.ledgerAmt("")+`),0) FROM cost_ledger WHERE day >= ? GROUP BY day ORDER BY day DESC LIMIT 90`, since)
	if err == nil {
		for drows.Next() {
			var d day
			if drows.Scan(&d.Day, &d.USD) == nil {
				series = append([]day{d}, series...)
			}
		}
		drows.Close()
	}
	writeJSON(w, 200, map[string]any{"rows": out, "days": series, "total_usd": total, "since": since,
		"currency": s.fx().Display, "estimated_runs": estimated})
}
