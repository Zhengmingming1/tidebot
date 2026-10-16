package webui

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Version is shown in the UI footer.
var Version = buildinfo.Version

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.withAuthCSRF(s.handleLogout))
	mux.HandleFunc("/api/me", s.withAuthCSRF(s.handleMe))
	mux.HandleFunc("/api/overview", s.withAuthCSRF(s.handleOverview))
	mux.HandleFunc("/api/tasks", s.withAuthCSRF(s.handleTasks))
	mux.HandleFunc("/api/tasks/", s.withAuthCSRF(s.handleTaskSub))
	mux.HandleFunc("/api/amendments/", s.withAuthCSRF(s.handleAmendAction))
	mux.HandleFunc("/api/skills", s.withAuthCSRF(s.handleSkills))
	mux.HandleFunc("/api/skills/import", s.withAuthCSRF(s.handleSkillsImport))
	mux.HandleFunc("/api/skills/upload", s.withAuthCSRF(s.handleSkillUpload))
	mux.HandleFunc("/api/providers", s.withAuthCSRF(s.handleProviders))
	mux.HandleFunc("/api/plugins/upload", s.withAuthCSRF(s.handlePluginUpload))
	mux.HandleFunc("/api/providers/", s.withAuthCSRF(s.handleProviderSub))
	mux.HandleFunc("/api/skills/", s.withAuthCSRF(s.handleSkillSub))
	mux.HandleFunc("/api/runs", s.withAuthCSRF(s.handleRuns))
	mux.HandleFunc("/api/runs/quick", s.withAuthCSRF(s.handleQuickRun))
	mux.HandleFunc("/api/runs/", s.withAuthCSRF(s.handleRunSub))
	mux.HandleFunc("/api/buttons", s.withAuthCSRF(s.handleButtons))
	mux.HandleFunc("/api/buttons/", s.withAuthCSRF(s.handleButtonRun))
	mux.HandleFunc("/api/schedule", s.withAuthCSRF(s.handleSchedule))
	mux.HandleFunc("/api/dashboard", s.withAuthCSRF(s.handleDashboard))
	mux.HandleFunc("/api/scheduler/preview", s.withAuthCSRF(s.handleCronPreview))
	mux.HandleFunc("/api/schedule/preview", s.withAuthCSRF(s.handleCronPreview))
	mux.HandleFunc("/api/spend", s.withAuthCSRF(s.handleSpend))
	mux.HandleFunc("/api/spend/breakdown", s.withAuthCSRF(s.handleSpendBreakdown))
	mux.HandleFunc("/api/claude/status", s.withAuthCSRF(s.handleClaudeStatus))
	mux.HandleFunc("/api/desktop/pick-file", s.withAuthCSRF(s.handlePickFile))
	mux.HandleFunc("/api/notifications/center", s.withAuthCSRF(s.handleNotifCenter))
	mux.HandleFunc("/api/auth/2fa/", s.withAuthCSRF(s.handle2FA))
	mux.HandleFunc("/api/notifications", s.withAuthCSRF(s.handleNotifications))
	mux.HandleFunc("/api/search", s.withAuthCSRF(s.handleSearch))
	mux.HandleFunc("/api/settings", s.withAuthCSRF(s.handleSettings))
	mux.HandleFunc("/api/webui", s.withAuthCSRF(s.handleWebUIToggle))
	mux.HandleFunc("/api/budgets", s.withAuthCSRF(s.handleBudgets))
	mux.HandleFunc("/api/settings/language", s.withAuthCSRF(s.handleLanguage))
	mux.HandleFunc("/api/rpc/", s.withAuthCSRF(s.handleExt))
}

func (s *Server) loc() *time.Location {
	if s.deps.Location != nil {
		return s.deps.Location
	}
	return time.Local
}

// ---------- DTOs ----------

type taskDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Cron        string   `json:"cron"`
	Prompt      string   `json:"prompt"`
	Goal        string   `json:"goal"`
	Provider    string   `json:"provider"`
	Workspace   string   `json:"workspace"`
	NextRunAt   string   `json:"next_run_at"`
	LastFiredAt string   `json:"last_fired_at"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	VersionNo   int      `json:"version_no"`
	VersionID   string   `json:"version_id"`
	Constraints []string `json:"constraints"`
	// ConstraintIDs parallels Constraints; amendment items refer to constraints by id.
	ConstraintIDs []string `json:"constraint_ids"`
	Skills        []string `json:"skills"`
	RunsToday     int      `json:"runs_today"`
	LastStatus    string   `json:"last_status"`
}

func toTaskDTO(v tasksvc.TaskView) taskDTO {
	d := taskDTO{
		ID: v.Task.ID, Name: v.Task.Name, Status: v.Task.Status, Cron: v.Task.CronExpr,
		Prompt: v.Task.Prompt, Workspace: v.Task.WorkspaceDir,
		NextRunAt: v.Task.NextRunAt.String, LastFiredAt: v.Task.LastFiredAt.String,
		CreatedAt: v.Task.CreatedAt, UpdatedAt: v.Task.UpdatedAt,
		Constraints: []string{}, ConstraintIDs: []string{}, Skills: []string{},
	}
	if v.CurrentVersion != nil {
		d.VersionNo = v.CurrentVersion.VersionNo
		d.VersionID = v.CurrentVersion.ID
	}
	if v.Spec != nil {
		d.Goal = v.Spec.Goal
		d.Provider = v.Spec.Provider
		if v.Spec.Prompt != "" {
			d.Prompt = v.Spec.Prompt
		}
		if v.Spec.WorkspaceDir != "" {
			d.Workspace = v.Spec.WorkspaceDir
		}
		for _, c := range v.Spec.Constraints {
			d.Constraints = append(d.Constraints, c.Text)
			d.ConstraintIDs = append(d.ConstraintIDs, c.ID)
		}
		for _, b := range v.Spec.SkillBindings {
			d.Skills = append(d.Skills, b.SkillName)
		}
	}
	return d
}

type runDTO struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"task_id"`
	TaskName     string  `json:"task_name"`
	Status       string  `json:"status"`
	Trigger      string  `json:"trigger"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	DurationMS   int64   `json:"duration_ms"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   string  `json:"finished_at"`
	CreatedAt    string  `json:"created_at"`
	Error        string  `json:"error"`
	Prompt       string  `json:"prompt"`
	VersionNo    int     `json:"version_no"`
	Bucket       string  `json:"bucket"`
}

const runSelect = `SELECT r.id, COALESCE(r.task_id,''), COALESCE(t.name,''), r.status, r.trigger_kind,
 COALESCE(r.provider_name,''), COALESCE(r.model,''), COALESCE(r.total_cost_usd,0),
 COALESCE(r.input_tokens,0), COALESCE(r.output_tokens,0), COALESCE(r.duration_ms,0),
 COALESCE(r.started_at,''), COALESCE(r.finished_at,''), r.created_at, COALESCE(r.error_message,''),
 r.prompt, COALESCE(v.version_no,0), COALESCE(r.cost_bucket,'production')
FROM runs r LEFT JOIN tasks t ON t.id = r.task_id LEFT JOIN versions v ON v.id = r.task_version_id`

func (s *Server) queryRuns(ctx context.Context, where string, args ...any) ([]runDTO, error) {
	rows, err := s.deps.DB.QueryContext(ctx, runSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []runDTO{}
	for rows.Next() {
		var d runDTO
		if err := rows.Scan(&d.ID, &d.TaskID, &d.TaskName, &d.Status, &d.Trigger, &d.Provider, &d.Model,
			&d.CostUSD, &d.InputTokens, &d.OutputTokens, &d.DurationMS, &d.StartedAt, &d.FinishedAt,
			&d.CreatedAt, &d.Error, &d.Prompt, &d.VersionNo, &d.Bucket); err != nil {
			return nil, err
		}
		if len(d.Prompt) > 300 {
			d.Prompt = d.Prompt[:300] + "…"
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func limitParam(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// ---------- buttons ----------

type buttonDTO struct {
	ID          string        `json:"id"`
	Label       string        `json:"label"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	TaskID      string        `json:"task_id,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Provider    string        `json:"provider,omitempty"`
	Color       string        `json:"color,omitempty"`
	SkillID     string        `json:"skill_id,omitempty"`
	SkillVerID  string        `json:"skill_version_id,omitempty"`
	Params      []buttonParam `json:"params,omitempty"`
}

type buttonParam struct {
	Name    string `json:"name"`
	Default string `json:"default"`
}

var buttonColorRe = regexp.MustCompile(`^(|#[0-9a-fA-F]{6})$`)
var paramNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,31}$`)

// expandParams replaces {{name}} with the param default.
func expandParams(prompt string, ps []buttonParam) string {
	for _, p := range ps {
		prompt = strings.ReplaceAll(prompt, "{{"+p.Name+"}}", p.Default)
	}
	return prompt
}

var buttonIDRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

func (s *Server) loadButtons(ctx context.Context) []buttonDTO {
	raw, _ := s.deps.Auth.GetSetting(ctx, "buttons")
	out := []buttonDTO{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) handleButtons(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.loadButtons(r.Context()))
	case http.MethodPut, http.MethodPost:
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var list []buttonDTO
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&list); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			taken := map[string]bool{}
			for _, b := range list {
				if b.ID != "" {
					if taken[b.ID] {
						writeErr(w, 400, "duplicate button id "+b.ID)
						return
					}
					taken[b.ID] = true
				}
			}
			for i := range list {
				b := &list[i]
				if b.ID == "" {
					b.ID = NewButtonID(b.Label, taken)
					taken[b.ID] = true
				}
				if !buttonIDRe.MatchString(b.ID) || strings.TrimSpace(b.Label) == "" || (b.TaskID == "" && b.Prompt == "") {
					writeErr(w, 400, "each button needs a label and a task or a prompt")
					return
				}
				if !buttonColorRe.MatchString(b.Color) || len(b.Params) > 10 {
					writeErr(w, 400, "bad color or too many params")
					return
				}
				for _, p := range b.Params {
					if !paramNameRe.MatchString(p.Name) || len(p.Default) > 500 {
						writeErr(w, 400, "param names: letters, digits, _")
						return
					}
				}
			}
			bs, _ := json.Marshal(list)
			if err := s.deps.Auth.SetSetting(r.Context(), "buttons", string(bs)); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, list)
		})).ServeHTTP(w, r)
	default:
		writeErr(w, 405, "method")
	}
}

func (s *Server) handleButtonRun(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/buttons/"), "/")
	if len(parts) != 2 || parts[1] != "run" || r.Method != http.MethodPost {
		writeErr(w, 404, "not found")
		return
	}
	label, err := s.RunButton(r.Context(), parts[0])
	if err != nil {
		code := 500
		if label == "" {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true, "started": label})
}

func (s *Server) handleQuickRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt   string `json:"prompt"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Prompt) == "" {
		writeErr(w, 400, "prompt required")
		return
	}
	if s.deps.QuickRun == nil {
		writeErr(w, 500, "execution not available")
		return
	}
	if err := s.deps.QuickRun(body.Prompt, body.Provider); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true})
}

// ---------- schedule ----------

type scheduleItem struct {
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`
	At       string `json:"at"`
	Cron     string `json:"cron"`
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func (s *Server) upcoming(ctx context.Context, horizon time.Duration, max int) ([]scheduleItem, error) {
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT id, name, cron_expr FROM tasks WHERE status='active' AND cron_expr <> ''`)
	if err != nil {
		return nil, err
	}
	type t3 struct{ id, name, expr string }
	var list []t3
	for rows.Next() {
		var x t3
		if err := rows.Scan(&x.id, &x.name, &x.expr); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, x)
	}
	rows.Close()
	now := time.Now().In(s.loc())
	end := now.Add(horizon)
	out := []scheduleItem{}
	for _, x := range list {
		sch, err := cronParser.Parse(x.expr)
		if err != nil {
			continue
		}
		next := now
		for i := 0; i < 6; i++ {
			next = sch.Next(next)
			if next.IsZero() || next.After(end) {
				break
			}
			out = append(out, scheduleItem{TaskID: x.id, TaskName: x.name, At: next.UTC().Format(time.RFC3339), Cron: x.expr})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	items, err := s.upcoming(r.Context(), 7*24*time.Hour, limitParam(r, 20, 100))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "timezone": s.loc().String()})
}

// ---------- overview (home) ----------

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sched, _ := s.upcoming(ctx, 48*time.Hour, 4)
	runs, err := s.queryRuns(ctx, "ORDER BY r.created_at DESC LIMIT 8")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	sum, _ := s.deps.Cost.Summary(ctx, "")
	var maxUSD float64
	for _, b := range sum.Budgets {
		if b.Scope == "global" && b.Period == "day" {
			maxUSD = b.MaxUSD
		}
	}
	// budgets reset at next local midnight of the configured timezone
	reset := s.deps.Cost.ResetsAt()
	var active int
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs WHERE status IN ('queued','running')`).Scan(&active)
	writeJSON(w, 200, map[string]any{
		"buttons":  s.loadButtons(ctx),
		"schedule": sched,
		"runs":     runs,
		"spend": map[string]any{
			"used_usd": sum.TotalUSD, "max_usd": maxUSD, "tokens": sum.TotalTokens,
			"by_bucket": sum.ByBucket, "resets_at": reset.UTC().Format(time.RFC3339), "timezone": sum.Timezone,
		},
		"active_runs": active,
		"timezone":    s.loc().String(),
	})
}

// ---------- tasks ----------

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.createTask(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method")
		return
	}
	list, err := s.deps.Tasks.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []taskDTO{}
	for _, v := range list {
		if v.Task.Status == "archived" {
			continue
		}
		out = append(out, toTaskDTO(v))
	}
	// enrich with last status / runs today
	today := time.Now().UTC().Format("2006-01-02")
	for i := range out {
		_ = s.deps.DB.QueryRowContext(r.Context(), `SELECT status FROM runs WHERE task_id=? ORDER BY created_at DESC LIMIT 1`, out[i].ID).Scan(&out[i].LastStatus)
		_ = s.deps.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM runs WHERE task_id=? AND substr(created_at,1,10)=?`, out[i].ID, today).Scan(&out[i].RunsToday)
	}
	writeJSON(w, 200, out)
}

type versionDTO struct {
	ID        string `json:"id"`
	No        int    `json:"no"`
	ParentID  string `json:"parent_id"`
	Source    string `json:"source"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleTaskSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	ctx := r.Context()
	if id == "batch" && len(parts) == 1 && r.Method == http.MethodPost {
		s.handleTaskBatch(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		s.updateTask(w, r, id)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if _, err := s.deps.Tasks.Get(ctx, id); err != nil {
			writeErr(w, 404, "task not found")
			return
		}
		// soft delete: history, versions and spend stay for the audit trail
		if err := s.deps.Tasks.SetStatus(ctx, id, "archived", "deleted in WebUI"); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		_ = s.deps.Tasks.SetSchedule(ctx, id, "")
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "form" {
		f, err := s.taskFormOf(ctx, id)
		if err != nil {
			writeErr(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, f)
		return
	}
	if len(parts) == 1 {
		v, err := s.deps.Tasks.Get(ctx, id)
		if err != nil {
			writeErr(w, 404, "task not found")
			return
		}
		writeJSON(w, 200, toTaskDTO(v))
		return
	}
	switch parts[1] {
	case "amendments-confirm":
		// Confirm selected pending items across messages of this task in one go: one new version.
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil || len(body.IDs) == 0 || len(body.IDs) > 100 {
				writeErr(w, 400, "ids required (1-100)")
				return
			}
			for _, iid := range body.IDs {
				var tid string
				if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT task_id FROM requirement_items WHERE id=?`, iid).Scan(&tid); err != nil || tid != id {
					writeErr(w, 400, "item does not belong to this task")
					return
				}
			}
			res, err := s.deps.Amend.ConfirmItems(r.Context(), body.IDs, "user")
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, res)
		})).ServeHTTP(w, r)
		return
	case "amendments":
		if r.Method == http.MethodGet {
			list, err := s.deps.Amend.List(ctx, id, "")
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, list)
			return
		}
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Utterance string `json:"utterance"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Utterance) == "" {
				writeErr(w, 400, "utterance required")
				return
			}
			items, err := s.deps.Amend.Propose(r.Context(), id, body.Utterance, "user")
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, items)
		})).ServeHTTP(w, r)
		return
	case "versions":
		vers, err := s.deps.Ver.ListVersions(ctx, version.EntityTask, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		cur, _ := s.deps.Ver.GetPointer(ctx, version.EntityTask, id, version.PointerCurrent)
		out := []versionDTO{}
		for _, v := range vers {
			out = append(out, versionDTO{ID: v.ID, No: v.VersionNo, ParentID: v.ParentVersionID.String,
				Source: v.Source, Message: v.Message.String, CreatedAt: v.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"versions": out, "current": cur.String})
		return
	case "runs":
		runs, err := s.queryRuns(ctx, "WHERE r.task_id=? ORDER BY r.created_at DESC LIMIT ?", id, limitParam(r, 10, 100))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, runs)
		return
	case "run":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || s.deps.RunTask == nil {
				writeErr(w, 405, "method")
				return
			}
			if err := s.deps.RunTask(id, cost.BucketProduction); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 202, map[string]any{"ok": true})
		})).ServeHTTP(w, r)
		return
	case "pause", "resume":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := "paused"
			if parts[1] == "resume" {
				st = "active"
			}
			if err := s.deps.Tasks.SetStatus(r.Context(), id, st, "webui"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "status": st})
		})).ServeHTTP(w, r)
		return
	case "rollback":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				VersionID string `json:"version_id"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
			if body.VersionID == "" {
				writeErr(w, 400, "version_id required")
				return
			}
			if err := s.deps.Tasks.Rollback(r.Context(), id, body.VersionID, "user", "webui rollback"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		})).ServeHTTP(w, r)
		return
	}
	writeErr(w, 404, "not found")
}

func (s *Server) handleAmendAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/amendments/"), "/")
	if len(parts) < 2 || r.Method != http.MethodPost {
		writeErr(w, 404, "not found")
		return
	}
	id, action := parts[0], parts[1]
	var bb struct {
		Only []string `json:"only"`
	}
	switch action {
	case "confirm-batch":
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&bb)
		res, err := s.deps.Amend.ConfirmBatch(r.Context(), id, bb.Only, "user")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)
	case "reject-batch":
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&bb)
		n, err := s.deps.Amend.RejectBatch(r.Context(), id, bb.Only)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"rejected": n})
	case "confirm":
		a, err := s.deps.Amend.Confirm(r.Context(), id, "user")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, a)
	case "edit":
		var body struct {
			Content string `json:"content"`
			Scope   string `json:"scope"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body)
		it, err := s.deps.Amend.Edit(r.Context(), id, body.Content, body.Scope)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, it)
	case "reject":
		if err := s.deps.Amend.Reject(r.Context(), id); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "resolve":
		var body struct {
			Resolution string `json:"resolution"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
		if err := s.deps.Amend.ResolveConflict(r.Context(), id, body.Resolution); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 404, "not found")
	}
}

// ---------- skills ----------

type skillDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	VersionID string `json:"version_id"`
	VersionNo int    `json:"version_no"`
	UpdatedAt string `json:"updated_at"`
	Files     int    `json:"files"`
	UsedBy    int    `json:"used_by"`
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Skills.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []skillDTO{}
	for _, sk := range list {
		d := skillDTO{ID: sk.ID, Name: sk.Name}
		if sk.CurrentVersion != nil {
			d.VersionID = sk.CurrentVersion.ID
			d.VersionNo = sk.CurrentVersion.VersionNo
			d.UpdatedAt = sk.CurrentVersion.CreatedAt
			_ = s.deps.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM version_files WHERE version_id=?`, d.VersionID).Scan(&d.Files)
		}
		out = append(out, d)
	}
	// used_by: tasks whose current spec binds this skill
	tasks, _ := s.deps.Tasks.List(r.Context())
	for i := range out {
		for _, t := range tasks {
			if t.Spec == nil {
				continue
			}
			for _, b := range t.Spec.SkillBindings {
				if b.SkillID == out[i].ID {
					out[i].UsedBy++
				}
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSkillSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/skills/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	switch parts[1] {
	case "versions":
		vers, err := s.deps.Ver.ListVersions(r.Context(), version.EntitySkill, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		cur, _ := s.deps.Ver.GetPointer(r.Context(), version.EntitySkill, id, version.PointerCurrent)
		can, _ := s.deps.Ver.GetPointer(r.Context(), version.EntitySkill, id, version.PointerCanary)
		out := []versionDTO{}
		for _, v := range vers {
			out = append(out, versionDTO{ID: v.ID, No: v.VersionNo, ParentID: v.ParentVersionID.String, Source: v.Source, Message: v.Message.String, CreatedAt: v.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"versions": out, "current": cur.String, "canary": can.String})
	case "diff":
		s.handleSkillDiff(w, r, id)
	case "files":
		s.handleSkillFiles(w, r, id)
	case "rollback":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				VersionID string `json:"version_id"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
			if body.VersionID == "" {
				writeErr(w, 400, "version_id required")
				return
			}
			if err := s.deps.Skills.Rollback(r.Context(), id, body.VersionID, "user"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		})).ServeHTTP(w, r)
	default:
		writeErr(w, 404, "not found")
	}
}

func (s *Server) handleSkillsImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Dir     string `json:"dir"`
		StageID string `json:"stage_id"`
		Mode    string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil || (body.Dir == "" && body.StageID == "") {
		writeErr(w, 400, "stage_id or dir required")
		return
	}
	if body.StageID != "" {
		s.importStaged(w, r, body.StageID, body.Name, body.Mode)
		return
	}
	if st, err := os.Stat(body.Dir); err != nil || !st.IsDir() {
		writeErr(w, 400, "dir not found")
		return
	}
	sk, err := s.deps.Skills.ImportDir(r.Context(), body.Name, body.Dir, "user")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": sk.ID, "name": sk.Name})
}

// ---------- runs ----------

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := "WHERE 1=1"
	var args []any
	if st := q.Get("status"); st != "" {
		where += " AND r.status=?"
		args = append(args, st)
	}
	if tid := q.Get("task_id"); tid != "" {
		where += " AND r.task_id=?"
		args = append(args, tid)
	}
	if qs := strings.TrimSpace(q.Get("q")); qs != "" {
		if len(qs) > 100 {
			writeErr(w, 400, "search text too long")
			return
		}
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(qs) + "%"
		where += ` AND (r.prompt LIKE ? ESCAPE '\' OR t.name LIKE ? ESCAPE '\' OR r.id LIKE ? ESCAPE '\' OR r.error_message LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like)
	}
	order := map[string]string{"": "r.created_at DESC", "new": "r.created_at DESC", "old": "r.created_at ASC",
		"cost": "COALESCE(r.total_cost_usd,0) DESC, r.created_at DESC", "tokens": "(COALESCE(r.input_tokens,0)+COALESCE(r.output_tokens,0)) DESC, r.created_at DESC",
		"duration": "COALESCE(r.duration_ms,0) DESC, r.created_at DESC"}[q.Get("sort")]
	if order == "" {
		writeErr(w, 400, "sort must be new, old, cost, tokens or duration")
		return
	}
	where += " ORDER BY " + order + " LIMIT ?"
	args = append(args, limitParam(r, 50, 200))
	runs, err := s.queryRuns(r.Context(), where, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) runStatus(ctx context.Context, id string) string {
	var st string
	if err := s.deps.DB.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, id).Scan(&st); err != nil {
		return ""
	}
	return st
}

func (s *Server) handleRunSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		runs, err := s.queryRuns(r.Context(), "WHERE r.id=?", id)
		if err != nil || len(runs) == 0 {
			writeErr(w, 404, "run not found")
			return
		}
		writeJSON(w, 200, runs[0])
		return
	}
	if parts[1] == "rerun" && r.Method == http.MethodPost {
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.rerun(w, r, id) })).ServeHTTP(w, r)
		return
	}
	if parts[1] != "events" {
		writeErr(w, 404, "not found")
		return
	}
	s.streamRunLog(w, r, id)
}

func isTerminal(st string) bool {
	switch st {
	case "succeeded", "failed", "canceled", "timeout", "skipped":
		return true
	}
	return false
}

// streamRunLog tails the run's log file over SSE until the run ends,
// the client disconnects, or the UI goes idle.
func (s *Server) streamRunLog(w http.ResponseWriter, r *http.Request, runID string) {
	ctx := r.Context()
	st := s.runStatus(ctx, runID)
	if st == "" {
		writeErr(w, 404, "run not found")
		return
	}
	if !s.sseAllowed() {
		writeErr(w, 429, "sse idle; interact with UI to resume")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no flush")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// Each log line carries its end offset as the SSE id, so a reconnect (Last-Event-ID or ?from=)
	// continues where the client left off instead of replaying the whole log.
	var offset int64
	send := func(event, data string) {
		for _, line := range strings.Split(data, "\n") {
			if event != "" {
				fmt.Fprintf(w, "event: %s\n", event)
			} else {
				fmt.Fprintf(w, "id: %d\n", offset)
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		flusher.Flush()
	}
	path := ""
	if s.deps.LogPath != nil {
		path = s.deps.LogPath(runID)
	}
	if from := r.Header.Get("Last-Event-ID"); from != "" || r.URL.Query().Get("from") != "" {
		if from == "" {
			from = r.URL.Query().Get("from")
		}
		if n, err := strconv.ParseInt(from, 10, 64); err == nil && n > 0 {
			if fi, err := os.Stat(path); err == nil && n <= fi.Size() {
				offset = n
			}
		}
	}
	readMore := func() bool {
		if path == "" {
			return false
		}
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return false
		}
		br := bufio.NewReader(f)
		ended := false
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 && strings.HasSuffix(line, "\n") {
				offset += int64(len(line))
				line = strings.TrimRight(line, "\n")
				if line == "[[end]]" {
					ended = true
					continue
				}
				send("", line)
			}
			if err != nil {
				break
			}
		}
		return ended
	}
	if readMore() {
		send("end", s.runStatus(ctx, runID))
		return
	}
	if path == "" && isTerminal(st) {
		send("", "(no log captured for this run)")
		send("end", st)
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ticker.C:
			if readMore() {
				send("end", s.runStatus(ctx, runID))
				return
			}
			if cur := s.runStatus(ctx, runID); isTerminal(cur) {
				readMore()
				send("end", cur)
				return
			}
			if !s.sseAllowed() {
				send("idle", "stream paused (idle)")
				return
			}
		}
	}
}

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
			_ = s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(usd),0), COALESCE(SUM(tokens),0) FROM cost_ledger WHERE created_at >= ? AND created_at < ?`, st, en).Scan(&d.USD, &d.Tokens)
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
		// Structured form of Detail so the UI can translate it (code: usd_cap | token_cap | zero).
		Code string `json:"code,omitempty"`
		Used string `json:"used,omitempty"`
		Cap  string `json:"cap,omitempty"`
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
	rows, err = s.deps.DB.QueryContext(ctx, `SELECT COALESCE(c.task_id,''), COALESCE(t.name,'(ad-hoc)'), SUM(c.usd), COUNT(1)
FROM cost_ledger c LEFT JOIN tasks t ON t.id=c.task_id WHERE c.created_at >= ? AND c.created_at < ? GROUP BY c.task_id ORDER BY SUM(c.usd) DESC LIMIT 8`, dst, den)
	if err == nil {
		for rows.Next() {
			var t taskSpend
			if rows.Scan(&t.TaskID, &t.Name, &t.USD, &t.Runs) == nil {
				top = append(top, t)
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"summary": sum, "days": days, "events": events, "top_tasks": top})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	type n struct {
		ID        string `json:"id"`
		Event     string `json:"event"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	out := []n{}
	rows, err := s.deps.DB.QueryContext(r.Context(), `SELECT id, event, body, created_at FROM notifications ORDER BY created_at DESC LIMIT 20`)
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
	sum, _ := s.deps.Cost.Summary(r.Context(), "")
	totp, _ := s.deps.Auth.TOTPEnabled(r.Context())
	writeJSON(w, 200, map[string]any{
		"webui_enabled":    s.Enabled(),
		"addr":             s.deps.Cfg.WebUIAddr,
		"allow_lan":        s.deps.Cfg.WebUIAllowLAN,
		"tls":              s.deps.Cfg.WebUITLSCert != "",
		"idle_minutes":     s.deps.Cfg.WebUIIdleMinutes,
		"providers":        providers,
		"default_provider": s.deps.Cfg.DefaultProvider,
		"budgets":          sum.Budgets,
		"totp_enabled":     totp,
		"timezone":         s.loc().String(),
		"version":          Version,
	})
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
	var body struct {
		Scope     string  `json:"scope"`
		ScopeID   string  `json:"scope_id"`
		Period    string  `json:"period"`
		Bucket    string  `json:"bucket"`
		MaxUSD    float64 `json:"max_usd"`
		MaxTokens int64   `json:"max_tokens"`
		Action    string  `json:"action"`
		WarnAtPct int     `json:"warn_at_pct"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// pause_notify: block the run and pause the task; block_notify: block the run, keep the task on.
	if body.Action == "" {
		body.Action = "pause_notify"
	}
	if body.Action != "pause_notify" && body.Action != "block_notify" {
		writeErr(w, 400, "action must be pause_notify or block_notify")
		return
	}
	if body.WarnAtPct == 0 {
		body.WarnAtPct = 80
	}
	if body.WarnAtPct < 1 || body.WarnAtPct > 100 {
		writeErr(w, 400, "warn_at_pct must be 1-100")
		return
	}
	if body.Scope == "" {
		body.Scope = "global"
	}
	if body.Period == "" {
		body.Period = "day"
	}
	switch body.Scope {
	case "global", "task", "bucket":
	default:
		writeErr(w, 400, "bad scope")
		return
	}
	b := cost.Budget{
		Scope: body.Scope, ScopeID: body.ScopeID, Period: body.Period, Bucket: body.Bucket,
		MaxUSD:    sql.NullFloat64{Float64: body.MaxUSD, Valid: body.MaxUSD > 0},
		MaxTokens: sql.NullInt64{Int64: body.MaxTokens, Valid: body.MaxTokens > 0},
		Action:    body.Action, WarnAtPct: body.WarnAtPct,
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

var (
	reUSDCap   = regexp.MustCompile(`daily USD cap reached \(([\d.]+)/([\d.]+)\)`)
	reTokenCap = regexp.MustCompile(`daily token cap reached \((\d+)/(\d+)\)`)
)

// parseBudgetDetail turns the stored English breach text into a code + numbers.
func parseBudgetDetail(d string) (code, used, limit string) {
	if m := reUSDCap.FindStringSubmatch(d); m != nil {
		return "usd_cap", m[1], m[2]
	}
	if m := reTokenCap.FindStringSubmatch(d); m != nil {
		return "token_cap", m[1], m[2]
	}
	if strings.Contains(d, "remaining daily budget is zero") {
		return "zero", "", ""
	}
	return "", "", ""
}

func (s *Server) handleExt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.deps.Ext == nil {
		writeErr(w, http.StatusNotFound, "not available")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	out, err := s.deps.Ext(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/rpc/"), json.RawMessage(raw))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": out})
}
