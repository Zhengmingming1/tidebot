package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"io"
	"math"
	"net/http"

	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// ---------- spend / notifications / search / settings ----------

func (s *Server) handleSpend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sum, err := s.deps.Cost.Summary(ctx, "")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// last 14 days
	type dayRow struct {
		Day    string  `json:"day"`
		USD    float64 `json:"usd"`
		Tokens int64   `json:"tokens"`
	}
	days := []dayRow{}
	today, _ := time.ParseInLocation("2006-01-02", sum.Day, s.deps.Cost.Location())
	for i := 13; i >= 0; i-- {
		d := dayRow{Day: today.AddDate(0, 0, -i).Format("2006-01-02")}
		if st, en, err := s.deps.Cost.DayRange(d.Day); err == nil {
			_ = s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+s.ledgerAmt("")+`),0), COALESCE(SUM(tokens),0) FROM cost_ledger WHERE created_at >= ? AND created_at < ?`, st, en).Scan(&d.USD, &d.Tokens)
		}
		if d.USD > 0 || d.Tokens > 0 {
			days = append(days, d)
		}
	}
	type evRow struct {
		At     string `json:"at"`
		Scope  string `json:"scope"`
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Detail string `json:"detail"`
		Action string `json:"action"`
		// Structured form of Detail so the UI can translate it (code: usd_cap | token_cap | held | zero).
		Code string `json:"code,omitempty"`
		Used string `json:"used,omitempty"`
		Cap  string `json:"cap,omitempty"`
		// code held: what the runs going held, and how many there were
		Held string `json:"held,omitempty"`
		N    string `json:"n,omitempty"`
	}
	events := []evRow{}
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT e.at, e.scope, COALESCE(e.scope_id,''), COALESCE(t.name,''), e.kind, e.detail, e.action_taken
FROM budget_events e LEFT JOIN tasks t ON e.scope='task' AND t.id=e.scope_id ORDER BY e.at DESC LIMIT 10`)
	if err == nil {
		for rows.Next() {
			var e evRow
			var sid string
			if rows.Scan(&e.At, &e.Scope, &sid, &e.Name, &e.Kind, &e.Detail, &e.Action) == nil {
				if e.Name == "" {
					e.Name = sid
				} else if sid != "" {
					// show task name instead of raw id in the human-readable detail
					e.Detail = strings.ReplaceAll(e.Detail, sid, e.Name)
				}
				e.Code, e.Used, e.Cap = parseBudgetDetail(e.Detail)
				e.Held, e.N = parseHeldDetail(e.Detail)
				events = append(events, e)
			}
		}
		rows.Close()
	}
	// top tasks today
	type taskSpend struct {
		TaskID string  `json:"task_id"`
		Name   string  `json:"name"`
		USD    float64 `json:"usd"`
		Runs   int     `json:"runs"`
	}
	top := []taskSpend{}
	dst, den, _ := s.deps.Cost.DayRange(sum.Day)
	rows, err = s.deps.DB.QueryContext(ctx, `SELECT COALESCE(c.task_id,''), COALESCE(t.name,'(ad-hoc)'), SUM(`+s.ledgerAmt("c.")+`), COUNT(1)
FROM cost_ledger c LEFT JOIN tasks t ON t.id=c.task_id WHERE c.created_at >= ? AND c.created_at < ? GROUP BY c.task_id ORDER BY SUM(`+s.ledgerAmt("c.")+`) DESC LIMIT 8`, dst, den)
	if err == nil {
		for rows.Next() {
			var t taskSpend
			if rows.Scan(&t.TaskID, &t.Name, &t.USD, &t.Runs) == nil {
				top = append(top, t)
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"summary": sum, "days": days, "events": events, "top_tasks": top, "unlimited_notice": s.unlimitedNotice(r)})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	type n struct {
		ID        string `json:"id"`
		Event     string `json:"event"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	out := []n{}
	rows, err := s.deps.DB.QueryContext(r.Context(), `SELECT id, event, body, created_at FROM notifications WHERE cleared_at='' ORDER BY created_at DESC LIMIT 20`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var x n
		if rows.Scan(&x.ID, &x.Event, &x.Body, &x.CreatedAt) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	type hit struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Sub  string `json:"sub"`
	}
	out := []hit{}
	if len([]rune(q)) > 200 {
		writeErr(w, 400, "query too long")
		return
	}
	if q == "" {
		writeJSON(w, 200, out)
		return
	}
	like := "%" + strings.NewReplacer("%", `\%`, "_", `\_`).Replace(q) + "%"
	query := func(kind, sqlq string) {
		rows, err := s.deps.DB.QueryContext(r.Context(), sqlq, like, like)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var h hit
			if rows.Scan(&h.ID, &h.Name, &h.Sub) == nil {
				h.Kind = kind
				out = append(out, h)
			}
		}
	}
	query("task", `SELECT id, name, status FROM tasks WHERE name LIKE ? ESCAPE '\' OR id LIKE ? ESCAPE '\' LIMIT 10`)
	query("skill", `SELECT id, name, '' FROM skills WHERE name LIKE ? ESCAPE '\' OR id LIKE ? ESCAPE '\' LIMIT 10`)
	query("run", `SELECT id, id, status FROM runs WHERE id LIKE ? ESCAPE '\' OR prompt LIKE ? ESCAPE '\' ORDER BY created_at DESC LIMIT 10`)
	writeJSON(w, 200, out)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	type prow struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		AuthEnv string `json:"auth_env"`
		KeySet  bool   `json:"key_set"`
	}
	providers := []prow{}
	if s.deps.Providers != nil {
		for _, v := range s.deps.Providers.ListProviders(r.Context()) {
			providers = append(providers, prow{Name: v.Name, BaseURL: v.BaseURL, Model: v.Model, AuthEnv: v.AuthEnv, KeySet: v.KeySet})
		}
	} else if s.deps.Reg != nil {
		for _, p := range s.deps.Reg.List() {
			providers = append(providers, prow{Name: p.Name, BaseURL: p.BaseURL, Model: p.Model, AuthEnv: p.AuthEnv,
				KeySet: p.AuthEnv != "" && os.Getenv(p.AuthEnv) != ""})
		}
	}
	sum, err := s.deps.Cost.Summary(r.Context(), "")
	if err != nil {
		s.settingsFailure(w, r, "cost summary", err)
		return
	}
	totp, err := s.deps.Auth.TOTPEnabled(r.Context())
	if err != nil {
		s.settingsFailure(w, r, "two-factor status", err)
		return
	}
	pwSet, err := s.deps.Auth.HasPassword(r.Context())
	if err != nil {
		s.settingsFailure(w, r, "password status", err)
		return
	}
	others := 0
	sessions := []auth.SessionInfo{}
	if sess := auth.SessionFromContext(r.Context()); sess != nil {
		others = s.deps.Auth.OtherSessions(r.Context(), sess.ID)
		sessions, err = s.deps.Auth.ListSessions(r.Context(), sess.ID)
		if err != nil {
			s.settingsFailure(w, r, "session list", err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"password_set":     pwSet,
		"other_sessions":   others,
		"sessions":         sessions,
		"webui_enabled":    s.Enabled(),
		"addr":             s.deps.Cfg.WebUIAddr,
		"allow_lan":        s.deps.Cfg.WebUIAllowLAN,
		"tls":              s.deps.Cfg.WebUITLSCert != "",
		"idle_minutes":     s.deps.Cfg.WebUIIdleMinutes,
		"providers":        providers,
		"default_provider": s.deps.Cfg.DefaultProvider,
		"budgets":          sum.Budgets,
		"fx":               s.fx(),
		"fx_revision":      func() string { b, _ := json.Marshal(s.fx()); return string(b) }(),
		"totp_enabled":     totp,
		"timezone":         s.loc().String(),
		"tz_offset":        tzOffsetMin(s.loc(), time.Now()),
		"version":          Version,
		// can command runs be sandboxed here (P1): bubblewrap/socat on Linux, Seatbelt on macOS
		"sandbox": guard.DetectSandbox(runtime.GOOS, exec.LookPath),
	})
}

func (s *Server) settingsFailure(w http.ResponseWriter, r *http.Request, operation string, err error) {
	status := http.StatusInternalServerError
	if s.deps.Log != nil {
		s.deps.Log.Error("settings request failed", "path", r.URL.Path, "status", status, "operation", operation, "error", err)
	}
	writeErr(w, status, err.Error())
}

// tzOffsetMin is loc's UTC offset at t in minutes (east positive).
func tzOffsetMin(loc *time.Location, t time.Time) int {
	_, off := t.In(loc).Zone()
	return off / 60
}

func (s *Server) handleWebUIToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "enabled": body.Enabled})
	if !body.Enabled {
		go func() {
			time.Sleep(200 * time.Millisecond)
			_ = s.SetEnabled(context.Background(), false)
		}()
	}
}

func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	// Fields left out keep what the budget has now: changing the cap in Settings must not
	// reset "on breach" / "warn at" chosen on the spend page, and the other way round.
	var body struct {
		Scope     string   `json:"scope"`
		ScopeID   string   `json:"scope_id"`
		Period    string   `json:"period"`
		Bucket    string   `json:"bucket"`
		MaxUSD    *float64 `json:"max_usd"`
		MaxTokens *int64   `json:"max_tokens"`
		Action    *string  `json:"action"`
		WarnAtPct *int     `json:"warn_at_pct"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Scope == "" {
		body.Scope = "global"
	}
	if body.Period == "" {
		body.Period = "day"
	}
	switch body.Scope {
	case "global", "task":
	default:
		writeErr(w, 400, "bad scope")
		return
	}
	if body.Period != "day" && body.Period != "run" {
		writeErr(w, 400, "bad period")
		return
	}
	// Only one global daily money budget exists. Reject legacy clients rather
	// than silently storing caps that no longer have an effect.
	if body.Bucket != "" || (body.Scope == "global" && (body.ScopeID != "" || body.Period != "day" || body.MaxTokens != nil)) || (body.Scope == "task" && body.ScopeID == "") {
		writeErr(w, 400, "use the global daily money budget or task-level controls")
		return
	}
	b, err := s.deps.Cost.FindBudget(r.Context(), body.Scope, body.ScopeID, body.Period, body.Bucket)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 500, err.Error())
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		b = cost.Budget{Scope: body.Scope, ScopeID: body.ScopeID, Period: body.Period, Bucket: body.Bucket, Action: "pause_notify", WarnAtPct: 80}
	}
	if b.Scope == "global" {
		// A legacy restored row must not obstruct editing the remaining money cap.
		b.MaxTokens = sql.NullInt64{}
	}
	if b.Action == "" {
		b.Action = "pause_notify"
	}
	if b.WarnAtPct == 0 {
		b.WarnAtPct = 80
	}
	if body.MaxUSD != nil {
		v := *body.MaxUSD
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e6 {
			writeErr(w, 400, "max_usd must be 0-1000000")
			return
		}
		b.MaxUSD = sql.NullFloat64{Float64: math.Round(v*1e4) / 1e4, Valid: v > 0}
		b.Currency = "" // new amount is in the display currency
	}
	if body.MaxTokens != nil {
		v := *body.MaxTokens
		if v < 0 || v > 1e12 {
			writeErr(w, 400, "max_tokens must be 0-1000000000000")
			return
		}
		b.MaxTokens = sql.NullInt64{Int64: v, Valid: v > 0}
	}
	// pause_notify: block the run and pause the task; block_notify: block the run, keep the task on.
	if body.Action != nil && *body.Action != "" {
		if *body.Action != "pause_notify" && *body.Action != "block_notify" {
			writeErr(w, 400, "action must be pause_notify or block_notify")
			return
		}
		b.Action = *body.Action
	}
	if body.WarnAtPct != nil && *body.WarnAtPct != 0 {
		if *body.WarnAtPct < 1 || *body.WarnAtPct > 100 {
			writeErr(w, 400, "warn_at_pct must be 1-100")
			return
		}
		b.WarnAtPct = *body.WarnAtPct
	}
	if err := s.deps.Cost.UpsertBudget(r.Context(), b); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// Languages supported by the WebUI dictionaries.
var supportedLangs = map[string]bool{"auto": true, "en": true, "zh-CN": true}

func (s *Server) language(ctx context.Context) string {
	raw, _ := s.deps.Auth.GetSetting(ctx, "language")
	var v string
	if json.Unmarshal([]byte(raw), &v) == nil && supportedLangs[v] {
		return v
	}
	return "auto"
}

func (s *Server) handleLanguage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]string{"language": s.language(r.Context())})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Language string `json:"language"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || !supportedLangs[body.Language] {
			writeErr(w, 400, "language must be auto, en or zh-CN")
			return
		}
		bs, _ := json.Marshal(body.Language)
		if err := s.deps.Auth.SetSetting(r.Context(), "language", string(bs)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"language": body.Language})
	default:
		writeErr(w, 405, "method not allowed")
	}
}
