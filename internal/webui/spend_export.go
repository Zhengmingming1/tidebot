package webui

import (
	"bytes"
	"encoding/csv"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxExportRows caps one CSV download (one row per run).
const maxExportRows = 100000

var exportHead = map[string][]string{
	"zh": {"日期", "时间", "任务", "运行 ID", "分类", "服务商", "模型", "状态", "请求数", "输入 tokens", "输出 tokens", "缓存读 tokens", "缓存写 tokens", "金额", "币种", "计价方式"},
	"en": {"Date", "Time", "Task", "Run ID", "Bucket", "Provider", "Model", "Status", "Requests", "Input tokens", "Output tokens", "Cache read tokens", "Cache write tokens", "Amount", "Currency", "Pricing"},
}

var exportSource = map[string]map[string]string{
	"zh": {"priced": "按单价计算", "estimate": "估算", "claude": "Claude Code 报告", "subscription": "订阅套餐", "free": "免费", "legacy": "旧版估算"},
	"en": {"priced": "Priced", "estimate": "Estimate", "claude": "Reported by Claude Code", "subscription": "Subscription", "free": "Free", "legacy": "Legacy estimate"},
}

var exportStatus = map[string]string{"succeeded": "成功", "failed": "失败", "timeout": "超时", "canceled": "已取消",
	"running": "运行中", "queued": "排队中", "skipped": "已跳过"}

// csvSafe neutralizes spreadsheet formulas in free-text cells (task names, model ids…):
// a cell starting with = + - @ or a tab/CR is prefixed with ' (OWASP "CSV injection").
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func fmtAmount(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64)
}

// handleSpendExport: GET /api/spend/export?range=7d|30d|all&lang=zh|en → CSV, one row
// per run in the range, amounts in the display currency, times in the server zone.
func (s *Server) handleSpendExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	q := r.URL.Query()
	n, ok := map[string]int{"": 30, "7d": 7, "30d": 30, "all": 0}[q.Get("range")]
	if !ok {
		writeErr(w, 400, "range must be 7d, 30d or all")
		return
	}
	lang := "en"
	if strings.HasPrefix(q.Get("lang"), "zh") {
		lang = "zh"
	}
	loc := s.deps.Cost.Location()
	now := time.Now().In(loc)
	since := "0000-00-00"
	if n > 0 {
		since = now.AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	}
	ctx := r.Context()
	// The store runs with one connection: finish this before opening the row cursor.
	first := since
	if n == 0 {
		first = ""
		_ = s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(day),'') FROM cost_ledger`).Scan(&first)
		if first == "" {
			first = now.Format("2006-01-02")
		}
	}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT l.day, l.created_at, COALESCE(t.name,''), l.run_id, l.bucket,
  COALESCE(NULLIF(l.provider,''), r.provider_name, ''), COALESCE(NULLIF(l.model,''), r.model, ''), COALESCE(r.status,''),
  l.requests, l.input_tokens, l.output_tokens, l.cache_read_tokens, l.cache_write_tokens, `+s.ledgerAmt("l.")+`, l.source
FROM cost_ledger l LEFT JOIN runs r ON r.id = l.run_id LEFT JOIN tasks t ON t.id = l.task_id
WHERE l.day >= ? ORDER BY l.created_at ASC, l.run_id ASC LIMIT ?`, since, maxExportRows)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	name := "tidebot-spend_" + first + "_to_" + now.Format("2006-01-02") + ".csv"
	// Build the file in memory and only then write it: streaming would hold the store's
	// single connection for as long as a slow client takes to read.
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // BOM so Excel opens UTF-8 (Chinese task names) correctly
	cw := csv.NewWriter(&buf)
	_ = cw.Write(exportHead[lang])
	cur := s.fx().Display
	adhoc := map[string]string{"zh": "快速运行", "en": "Quick run"}[lang]
	for rows.Next() {
		var day, created, task, runID, bucket, prov, model, status, source string
		var req, in, out, cr, cwT int64
		var amt float64
		if err := rows.Scan(&day, &created, &task, &runID, &bucket, &prov, &model, &status, &req, &in, &out, &cr, &cwT, &amt, &source); err != nil {
			rows.Close()
			writeErr(w, 500, "query failed")
			return
		}
		at := created
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			at = t.In(loc).Format("15:04:05")
		}
		if task == "" {
			task = adhoc
		}
		if z := exportStatus[status]; lang == "zh" && z != "" {
			status = z
		}
		src := exportSource[lang][source]
		if src == "" {
			src = source
		}
		_ = cw.Write([]string{day, at, csvSafe(task), runID, csvSafe(bucket), csvSafe(prov), csvSafe(model), status,
			strconv.FormatInt(req, 10), strconv.FormatInt(in, 10), strconv.FormatInt(out, 10), strconv.FormatInt(cr, 10),
			strconv.FormatInt(cwT, 10), fmtAmount(amt), cur, csvSafe(src)})
	}
	scanErr := rows.Err()
	rows.Close()
	cw.Flush()
	if scanErr != nil {
		writeErr(w, 500, "query failed")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(200)
	_, _ = w.Write(buf.Bytes())
}
