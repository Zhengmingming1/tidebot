package webui

import (
	"errors"
	"net/http"
	"time"
)

// GET /api/spend/breakdown?range=7d|30d|all&by=task|model|provider|provider_model|tag[&day=YYYY-MM-DD]
// day narrows the rows and totals to one calendar day of the range (a bar picked in
// the chart); the per-day series still covers the whole range so the chart stays put.
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
		// task tags: a run of a task with two tags counts under both (LiteLLM's spend per
		// request tag works the same way); untagged tasks and quick runs share the '' row
		"tag": "COALESCE(tt.tag,'')",
	}[by]
	if keyExpr == "" {
		writeErr(w, 400, "by must be task, model, provider, provider_model or tag")
		return
	}
	loc := s.deps.Cost.Location()
	since := "0000-00-00"
	if n > 0 {
		since = time.Now().In(loc).AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	}
	pick, err := spendDay(q.Get("day"), since)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// rows: the whole range, or only the picked day ("all" reads the table straight through)
	rowsWhere, rowsArgs := "l.day >= ?", []any{since}
	if pick != "" {
		rowsWhere, rowsArgs = "l.day = ?", []any{pick}
	} else if n == 0 {
		rowsWhere, rowsArgs = "1=1", nil
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
		Tasks      int     `json:"tasks"` // distinct tasks behind the row
	}
	amt := s.ledgerAmt("l.")
	ctx := r.Context()
	// Sum the ledger first, then look up the (at most 50) task names: joining tasks and
	// runs on every ledger row cost ~300 ms at 50 000 runs. runs is joined only for keys
	// that fall back to the run's provider / model (rows written before the ledger had them).
	from, group := "cost_ledger l", "k"
	switch by {
	case "task", "":
	case "tag":
		// tags are compared without case (the form joins "Daily" to "daily"); group the same way
		from += " LEFT JOIN task_tags tt ON tt.task_id = l.task_id"
		group = "k COLLATE NOCASE"
	default:
		from += " LEFT JOIN runs r ON r.id = l.run_id"
	}
	agg := `SELECT ` + keyExpr + ` AS k, COUNT(1) AS n, COALESCE(SUM(` + amt + `),0) AS usd, COALESCE(SUM(l.tokens),0) AS tok,
  COALESCE(SUM(l.input_tokens),0) AS i, COALESCE(SUM(l.output_tokens),0) AS o, COALESCE(SUM(l.cache_read_tokens),0) AS cr, COALESCE(SUM(l.cache_write_tokens),0) AS cw,
  COALESCE(SUM(l.requests),0) AS req, SUM(CASE WHEN l.source IN ('estimate','claude') THEN 1 ELSE 0 END) AS est,
  SUM(CASE WHEN l.source='legacy' THEN 1 ELSE 0 END) AS leg, SUM(CASE WHEN l.source='subscription' THEN 1 ELSE 0 END) AS sub,
  COUNT(DISTINCT l.task_id) AS nt
FROM ` + from + ` WHERE ` + rowsWhere + ` GROUP BY ` + group + ` ORDER BY usd DESC, tok DESC LIMIT 50`
	name := "''"
	if by == "task" || by == "" {
		name = "COALESCE(t.name,'')"
	}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT a.k, `+name+`, a.n, a.usd, a.tok, a.i, a.o, a.cr, a.cw, a.req, a.est, a.leg, a.sub, a.nt
FROM (`+agg+`) a LEFT JOIN tasks t ON t.id = a.k ORDER BY a.usd DESC, a.tok DESC`, rowsArgs...)
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
			&x.Requests, &x.Estimated, &x.Legacy, &x.Sub, &x.Tasks) == nil {
			if by != "task" && by != "" {
				x.Name = x.Key
			}
			total += x.USD
			estimated += x.Estimated + x.Legacy
			out = append(out, x)
		}
	}
	rows.Close()
	// By tag the rows overlap (a task with several tags is in each), so their sum is not the
	// total: read the range's own total and tell the UI the shares may add up past 100 %.
	overlap := false
	if by == "tag" {
		sum := total
		total, estimated = 0, 0
		if err := s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+amt+`),0),
  COALESCE(SUM(CASE WHEN l.source IN ('estimate','claude','legacy') THEN 1 ELSE 0 END),0)
FROM cost_ledger l WHERE `+rowsWhere, rowsArgs...).Scan(&total, &estimated); err != nil {
			writeErr(w, 500, "query failed")
			return
		}
		overlap = sum > total+1e-9
	}
	type day struct {
		Day string  `json:"day"`
		USD float64 `json:"usd"`
	}
	series := []day{}
	// the chart shows 90 days at most: don't sum the days before that
	from90 := time.Now().In(loc).AddDate(0, 0, -89).Format("2006-01-02")
	if from90 < since {
		from90 = since
	}
	drows, err := s.deps.DB.QueryContext(ctx, `SELECT day, COALESCE(SUM(`+s.ledgerAmt("")+`),0) FROM cost_ledger WHERE day >= ? GROUP BY day ORDER BY day DESC LIMIT 90`, from90)
	if err == nil {
		for drows.Next() {
			var d day
			if drows.Scan(&d.Day, &d.USD) == nil {
				series = append([]day{d}, series...)
			}
		}
		drows.Close()
	}
	writeJSON(w, 200, map[string]any{"rows": out, "days": series, "total_usd": total, "since": since, "day": pick,
		"currency": s.fx().Display, "estimated_runs": estimated, "overlap": overlap})
}

// spendDay checks the optional day= of the spend breakdown / export: empty, or a real
// YYYY-MM-DD date that is not before the range start (since).
func spendDay(v, since string) (string, error) {
	if v == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil || t.Format("2006-01-02") != v {
		return "", errors.New("day must be YYYY-MM-DD")
	}
	if v < since {
		return "", errors.New("day is outside the range")
	}
	return v, nil
}
