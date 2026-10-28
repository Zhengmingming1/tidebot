package webui

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/textclip"
)

// Export caps for the free-text columns: a run's prompt or error can be long, and the whole
// file is built in memory (see handleRunsExport).
const (
	exportPromptMax = 4000
	exportErrorMax  = 2000
)

var runsExportHead = map[string][]string{
	"zh": {"日期", "开始时间", "结束时间", "任务", "运行 ID", "状态", "触发方式", "服务商", "模型", "任务版本", "用时（秒）", "请求数", "输入 tokens", "输出 tokens", "缓存读 tokens", "缓存写 tokens", "金额", "币种", "错误", "提示词"},
	"en": {"Date", "Started", "Finished", "Task", "Run ID", "Status", "Trigger", "Provider", "Model", "Task version", "Duration (s)", "Requests", "Input tokens", "Output tokens", "Cache read tokens", "Cache write tokens", "Amount", "Currency", "Error", "Prompt"},
}

var runsExportTrigger = map[string]map[string]string{
	"zh": {"cron": "定时", "manual": "手动", "quick": "快速运行", "button": "按钮", "webhook": "Webhook", "file_change": "文件变化", "optimization": "技能优化", "plugin": "插件"},
	"en": {"cron": "Schedule", "manual": "Manual", "quick": "Quick run", "button": "Button", "webhook": "Webhook", "file_change": "File change", "optimization": "Skill optimization", "plugin": "Plugin"},
}

// clip cuts s to at most n bytes on a rune boundary, marking the cut with "…".
func clip(s string, n int) string { return textclip.Ellipsis(s, n) }

// exportRun is one JSONL line: stable English keys, times as stored (RFC 3339, UTC).
type exportRun struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"task_id,omitempty"`
	Task         string  `json:"task"`
	Status       string  `json:"status"`
	Trigger      string  `json:"trigger"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	VersionNo    int     `json:"task_version,omitempty"`
	CreatedAt    string  `json:"created_at"`
	StartedAt    string  `json:"started_at,omitempty"`
	FinishedAt   string  `json:"finished_at,omitempty"`
	DurationMS   int64   `json:"duration_ms"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Error        string  `json:"error,omitempty"`
	Prompt       string  `json:"prompt"`
}

// handleRunsExport: GET /api/runs/export?format=csv|jsonl&lang=zh|en plus the runs list's own
// filters (status, task_id, q, range / from / to) and sort — the file holds exactly the runs
// the list is showing, all of them rather than the loaded pages (up to maxExportRows).
// CSV is for spreadsheets (BOM, localized headers, local times, formula-safe cells); JSONL is
// for scripts (one object per line, English keys, UTC times). Amounts are in the display currency.
func (s *Server) handleRunsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "jsonl" {
		writeErr(w, 400, "format must be csv or jsonl")
		return
	}
	lang := "en"
	if strings.HasPrefix(q.Get("lang"), "zh") {
		lang = "zh"
	}
	loc, now := s.loc(), time.Now()
	where, args, err := runsFilter(q, true, loc, now)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	order := s.runsOrder(q.Get("sort"))
	if order == "" {
		writeErr(w, 400, "sort must be new, old, cost, tokens or duration")
		return
	}
	name := "tidebot-runs_" + exportSpan(q, loc, now) + "." + format
	rows, err := s.deps.DB.QueryContext(r.Context(), s.runSelect()+" "+where+" ORDER BY "+order+" LIMIT ?", append(args, maxExportRows)...)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	// Built in memory, then written: streaming would hold the store's single connection for
	// as long as a slow client takes to read.
	var buf bytes.Buffer
	var cw *csv.Writer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if format == "csv" {
		buf.WriteString("\xEF\xBB\xBF") // BOM so Excel reads UTF-8 (Chinese task names)
		cw = csv.NewWriter(&buf)
		_ = cw.Write(runsExportHead[lang])
	}
	cur := s.fx().Display
	adhoc := map[string]string{"zh": "快速运行", "en": "Quick run"}[lang]
	local := func(ts, layout string) string {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return t.In(loc).Format(layout)
		}
		return ts
	}
	for rows.Next() {
		d, err := scanRun(rows)
		if err != nil {
			rows.Close()
			writeErr(w, 500, "query failed")
			return
		}
		d.Prompt, d.Error = clip(d.Prompt, exportPromptMax), clip(d.Error, exportErrorMax)
		if cw == nil {
			_ = enc.Encode(exportRun{ID: d.ID, TaskID: d.TaskID, Task: d.TaskName, Status: d.Status, Trigger: d.Trigger,
				Provider: d.Provider, Model: d.Model, VersionNo: d.VersionNo, CreatedAt: d.CreatedAt, StartedAt: d.StartedAt,
				FinishedAt: d.FinishedAt, DurationMS: d.DurationMS, Requests: d.Requests, InputTokens: d.InputTokens,
				OutputTokens: d.OutputTokens, CacheRead: d.CacheRead, CacheWrite: d.CacheWrite, Amount: roundAmt(d.CostUSD),
				Currency: cur, Error: d.Error, Prompt: d.Prompt})
			continue
		}
		task := d.TaskName
		if task == "" {
			task = adhoc
		}
		status := d.Status
		if z := exportStatus[status]; lang == "zh" && z != "" {
			status = z
		}
		trig := runsExportTrigger[lang][d.Trigger]
		if trig == "" {
			trig = d.Trigger
		}
		start := d.StartedAt
		if start == "" {
			start = d.CreatedAt
		}
		fin, dur, ver := "", "", ""
		if d.FinishedAt != "" {
			fin = local(d.FinishedAt, "2006-01-02 15:04:05")
		}
		if d.DurationMS > 0 {
			dur = strconv.FormatFloat(float64(d.DurationMS)/1000, 'f', 1, 64)
		}
		if d.VersionNo > 0 {
			ver = "v" + strconv.Itoa(d.VersionNo)
		}
		i64 := func(n int64) string { return strconv.FormatInt(n, 10) }
		_ = cw.Write([]string{local(d.CreatedAt, "2006-01-02"), local(start, "15:04:05"), fin, csvSafe(task), d.ID, status, csvSafe(trig),
			csvSafe(d.Provider), csvSafe(d.Model), ver, dur, i64(d.Requests), i64(d.InputTokens), i64(d.OutputTokens),
			i64(d.CacheRead), i64(d.CacheWrite), fmtAmount(d.CostUSD), cur, csvSafe(oneLine(d.Error)), csvSafe(d.Prompt)})
	}
	scanErr := rows.Err()
	rows.Close()
	if cw != nil {
		cw.Flush()
	}
	if scanErr != nil {
		writeErr(w, 500, "query failed")
		return
	}
	h := w.Header()
	if format == "csv" {
		h.Set("Content-Type", "text/csv; charset=utf-8")
	} else {
		h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
	}
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(200)
	_, _ = w.Write(buf.Bytes())
}

func roundAmt(v float64) float64 {
	f, _ := strconv.ParseFloat(fmtAmount(v), 64)
	return f
}

// oneLine folds an error's line breaks (and runs of spaces) so a spreadsheet row stays one line high.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// exportSpan names the file after the list's time filter: "2026-10-20_to_2026-10-26",
// "2026-10-27" for one day, "all_to_<today>" without one. Days in the scheduling zone.
func exportSpan(q url.Values, loc *time.Location, now time.Time) string {
	today := now.In(loc).Format("2006-01-02")
	from, to, ok, err := runsRange(q, loc, now)
	if err != nil || !ok {
		return "all_to_" + today
	}
	a, b := from.In(loc).Format("2006-01-02"), to.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	if from.Year() <= 1970 {
		a = "start"
	}
	if a == b {
		return a
	}
	return a + "_to_" + b
}
