package webui

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
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
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/textclip"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Version is shown in the UI footer.
var Version = buildinfo.Version

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.withAuthCSRF(s.handleLogout))
	mux.HandleFunc("/api/me", s.withAuthCSRF(s.handleMe))
	mux.HandleFunc("/api/overview", s.withAuthCSRF(s.handleOverview))
	mux.HandleFunc("/api/rev", s.withAuthCSRF(s.handleRev))
	mux.HandleFunc("/api/tasks", s.withAuthCSRF(s.handleTasks))
	mux.HandleFunc("/api/tasks/", s.withAuthCSRF(s.handleTaskSub))
	mux.HandleFunc("/api/amendments/", s.withAuthCSRF(s.handleAmendAction))
	mux.HandleFunc("/api/skills", s.withAuthCSRF(s.handleSkills))
	mux.HandleFunc("/api/skills/import", s.withAuthCSRF(s.handleSkillsImport))
	mux.HandleFunc("/api/skills/upload", s.withAuthCSRF(s.handleSkillUpload))
	mux.HandleFunc("/api/providers", s.withAuthCSRF(s.handleProviders))
	mux.HandleFunc("/api/providers/infer", s.withAuthCSRF(postOnly(s.handleProviderInfer)))
	mux.HandleFunc("/api/plugins/prefill", s.withAuthCSRF(postOnly(s.handlePluginPrefill)))
	mux.HandleFunc("/api/tasks/workspace-check", s.withAuthCSRF(s.handleWorkspaceCheck))
	mux.HandleFunc("/api/tasks/infer", s.withAuthCSRF(postOnly(s.handleTaskInfer)))
	mux.HandleFunc("/api/plugins/upload", s.withAuthCSRF(s.handlePluginUpload))
	mux.HandleFunc("/api/providers/", s.withAuthCSRF(s.handleProviderSub))
	mux.HandleFunc("/api/skills/", s.withAuthCSRF(s.handleSkillSub))
	mux.HandleFunc("/api/runs", s.withAuthCSRF(s.handleRuns))
	mux.HandleFunc("/api/runs/quick", s.withAuthCSRF(s.handleQuickRun))
	mux.HandleFunc("/api/runs/recent-prompts", s.withAuthCSRF(s.handleRecentPrompts))
	mux.HandleFunc("/api/runs/counts", s.withAuthCSRF(s.handleRunCounts))
	mux.HandleFunc("/api/runs/export", s.withAuthCSRF(s.handleRunsExport))
	mux.HandleFunc("/api/runs/", s.withAuthCSRF(s.handleRunSub))
	mux.HandleFunc("/api/buttons", s.withAuthCSRF(s.handleButtons))
	mux.HandleFunc("/api/buttons/", s.withAuthCSRF(s.handleButtonRun))
	mux.HandleFunc("/api/schedule", s.withAuthCSRF(s.handleSchedule))
	mux.HandleFunc("/api/dashboard", s.withAuthCSRF(s.handleDashboard))
	mux.HandleFunc("/api/scheduler/preview", s.withAuthCSRF(s.handleCronPreview))
	mux.HandleFunc("/api/schedule/preview", s.withAuthCSRF(s.handleCronPreview))
	mux.HandleFunc("/api/spend", s.withAuthCSRF(s.handleSpend))
	mux.HandleFunc("/api/spend/breakdown", s.withAuthCSRF(s.handleSpendBreakdown))
	mux.HandleFunc("/api/spend/export", s.withAuthCSRF(s.handleSpendExport))
	mux.HandleFunc("/api/claude/status", s.withAuthCSRF(s.handleClaudeStatus))
	mux.HandleFunc("/api/desktop/pick-file", s.withAuthCSRF(s.handlePickFile))
	mux.HandleFunc("/api/desktop/pick-folder", s.withAuthCSRF(s.handlePickFolder))
	mux.HandleFunc("/api/desktop/save", s.withAuthCSRF(s.handleDesktopSave))
	mux.HandleFunc("/api/desktop/open", s.withAuthCSRF(s.handleDesktopOpen))
	mux.HandleFunc("/api/notifications/center", s.withAuthCSRF(s.handleNotifCenter))
	mux.HandleFunc("/api/auth/2fa/", s.withAuthCSRF(s.handle2FA))
	mux.HandleFunc("/api/auth/password", s.withAuthCSRF(s.handlePassword))
	mux.HandleFunc("/api/auth/sessions/others", s.withAuthCSRF(s.handleSessionsOthers))
	mux.HandleFunc("/api/notifications", s.withAuthCSRF(s.handleNotifications))
	mux.HandleFunc("/api/search", s.withAuthCSRF(s.handleSearch))
	mux.HandleFunc("/api/settings", s.withAuthCSRF(s.handleSettings))
	mux.HandleFunc("/api/webui", s.withAuthCSRF(s.handleWebUIToggle))
	mux.HandleFunc("/api/budgets", s.withAuthCSRF(s.handleBudgets))
	mux.HandleFunc("/api/settings/language", s.withAuthCSRF(s.handleLanguage))
	mux.HandleFunc("/api/rpc/", s.withAuthCSRF(s.handleExt))
}

func (s *Server) loc() *time.Location {
	if s.deps.LocFn != nil {
		if l := s.deps.LocFn(); l != nil {
			return l
		}
	}
	if s.deps.Location != nil {
		return s.deps.Location
	}
	return time.Local
}

// ---------- DTOs ----------

type taskDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Cron      string `json:"cron"`
	Prompt    string `json:"prompt"`
	Goal      string `json:"goal"`
	Provider  string `json:"provider"`
	Workspace string `json:"workspace"`
	// WorkspaceDefault: the task has no folder of its own and runs in the one tidebot
	// gives it (set on the detail answer only, where Workspace is then that folder).
	WorkspaceDefault bool     `json:"workspace_default,omitempty"`
	NextRunAt        string   `json:"next_run_at"`
	LastFiredAt      string   `json:"last_fired_at"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	VersionNo        int      `json:"version_no"`
	VersionID        string   `json:"version_id"`
	Constraints      []string `json:"constraints"`
	// ConstraintIDs parallels Constraints; amendment items refer to constraints by id.
	ConstraintIDs []string `json:"constraint_ids"`
	Skills        []string `json:"skills"`
	Tags          []string `json:"tags"`
	RunsToday     int      `json:"runs_today"`
	LastStatus    string   `json:"last_status"`
	// List answer only: when the newest run was created and the last few runs, oldest first.
	LastRunAt string `json:"last_run_at,omitempty"`
	Beats     []beat `json:"beats,omitempty"`
	// List answer only: the newest run that finished on its own (succeeded, failed or timed
	// out) failed; FailStreak counts the failed / timed-out ones in a row back from it.
	// Stopped, skipped, queued and running runs are passed over.
	Failing    bool `json:"failing,omitempty"`
	FailStreak int  `json:"fail_streak,omitempty"`
	// Paused tasks: why (user, breaker, budget, pack, plugin; "" = unknown, paused before
	// tidebot kept the reason), a short detail (error class, budget reason, plugin id) and when.
	PauseKind   string `json:"pause_kind,omitempty"`
	PauseDetail string `json:"pause_detail,omitempty"`
	PausedAt    string `json:"paused_at,omitempty"`
	BreakerAt   int    `json:"breaker_at,omitempty"` // breaker pauses: failures in a row that tripped it
}

func toTaskDTO(v tasksvc.TaskView) taskDTO {
	d := taskDTO{
		ID: v.Task.ID, Name: v.Task.Name, Status: v.Task.Status, Cron: v.Task.CronExpr,
		Prompt: v.Task.Prompt, Workspace: v.Task.WorkspaceDir,
		NextRunAt: v.Task.NextRunAt.String, LastFiredAt: v.Task.LastFiredAt.String,
		CreatedAt: v.Task.CreatedAt, UpdatedAt: v.Task.UpdatedAt,
		Constraints: []string{}, ConstraintIDs: []string{}, Skills: []string{}, Tags: []string{},
	}
	if v.Task.Status == "paused" {
		d.PauseKind, d.PauseDetail, d.PausedAt = v.Task.PauseKind, v.Task.PauseDetail, v.Task.PausedAt.String
		if d.PauseKind == tasksvc.PauseBreaker {
			d.BreakerAt = policy.BreakerThreshold
		}
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
	// tidebot's own pricing (native currency); CostUSD above is in the display currency.
	CostAmount   float64 `json:"cost_amount"`
	CostCurrency string  `json:"cost_currency"`
	CostSource   string  `json:"cost_source"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	Requests     int64   `json:"requests"`
	// QueuePos is a queued run's place in line for a free slot (1 = next).
	QueuePos int `json:"queue_pos,omitempty"`
	// Match: with search text, which field matched and an excerpt around the hit.
	Match *runHit `json:"match,omitempty"`
}

func (s *Server) runSelect() string {
	return `SELECT r.id, COALESCE(r.task_id,''), COALESCE(t.name,''), r.status, r.trigger_kind,
 COALESCE(r.provider_name,''), COALESCE(r.model,''), ` + s.runAmt("r.") + `,
 COALESCE(r.input_tokens,0), COALESCE(r.output_tokens,0), COALESCE(r.duration_ms,0),
 COALESCE(r.started_at,''), COALESCE(r.finished_at,''), r.created_at, COALESCE(r.error_message,''),
 r.prompt, COALESCE(v.version_no,0), COALESCE(r.cost_bucket,'production'),
 r.cost_amount, r.cost_currency, r.cost_source, r.cache_read_tokens, r.cache_write_tokens, r.requests
FROM runs r LEFT JOIN tasks t ON t.id = r.task_id LEFT JOIN versions v ON v.id = r.task_version_id`
}

// listPromptMax: lists carry a preview of each run's prompt (cut on a character boundary);
// the run detail and rerun read the whole prompt (queryRunsFull).
const listPromptMax = 300

func (s *Server) queryRuns(ctx context.Context, where string, args ...any) ([]runDTO, error) {
	return s.queryRunsOpt(ctx, listPromptMax, where, args...)
}

// queryRunsFull is queryRuns with the whole prompt — for one run's page and for rerunning it.
func (s *Server) queryRunsFull(ctx context.Context, where string, args ...any) ([]runDTO, error) {
	return s.queryRunsOpt(ctx, 0, where, args...)
}

func (s *Server) queryRunsOpt(ctx context.Context, promptMax int, where string, args ...any) ([]runDTO, error) {
	rows, err := s.deps.DB.QueryContext(ctx, s.runSelect()+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []runDTO{}
	for rows.Next() {
		d, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		if promptMax > 0 {
			d.Prompt = textclip.Ellipsis(d.Prompt, promptMax)
		}
		if d.Status == "queued" {
			d.QueuePos = s.queuePos(d.ID)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// scanRun reads one row of runSelect().
func scanRun(rows *sql.Rows) (runDTO, error) {
	var d runDTO
	err := rows.Scan(&d.ID, &d.TaskID, &d.TaskName, &d.Status, &d.Trigger, &d.Provider, &d.Model,
		&d.CostUSD, &d.InputTokens, &d.OutputTokens, &d.DurationMS, &d.StartedAt, &d.FinishedAt,
		&d.CreatedAt, &d.Error, &d.Prompt, &d.VersionNo, &d.Bucket,
		&d.CostAmount, &d.CostCurrency, &d.CostSource, &d.CacheRead, &d.CacheWrite, &d.Requests)
	return d, err
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
	label, runID, err := s.runButton(r.Context(), parts[0])
	if err != nil {
		code := 500
		if label == "" {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	out := map[string]any{"ok": true, "started": label}
	if runID != "" {
		out["run_id"] = runID
	}
	writeJSON(w, 202, out)
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
	runID, err := s.deps.QuickRun(body.Prompt, body.Provider)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.writeStarted(w, runID)
}

// handleRecentPrompts lists the last distinct quick-run prompts (newest first).
func (s *Server) handleRecentPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	list, err := s.deps.Store.RecentQuickPrompts(r.Context(), 6)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"prompts": list})
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
			next = scheduler.NextIn(sch, next, s.loc())
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
			"used_usd": sum.TotalUSD, "max_usd": maxUSD, "currency": sum.Currency, "tokens": sum.TotalTokens,
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
	if err := s.enrichTaskList(r.Context(), out); err != nil {
		writeErr(w, 500, err.Error())
		return
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
	if id == "export" && len(parts) == 1 && r.Method == http.MethodGet {
		s.exportTasks(w, r)
		return
	}
	if id == "import" && len(parts) == 1 && r.Method == http.MethodPost {
		s.importTasks(w, r)
		return
	}
	if id == "tags" && len(parts) == 1 && r.Method == http.MethodGet {
		s.handleTagsInUse(w, r)
		return
	}
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
	if len(parts) == 2 && parts[1] == "duplicate" && r.Method == http.MethodPost {
		s.duplicateTask(w, r, id)
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
		d := toTaskDTO(v)
		d.Workspace, d.WorkspaceDefault = s.taskWorkspace(d.ID, d.Workspace)
		if m, err := s.taskTags(ctx, id); err == nil && m[id] != nil {
			d.Tags = m[id]
		}
		writeJSON(w, 200, d)
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
			runID, err := s.deps.RunTask(id, cost.BucketProduction)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			s.writeStarted(w, runID)
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
	// Description is SKILL.md's frontmatter description (first 200 characters); the
	// button form uses it to prefill a button's subtitle.
	Description string `json:"description,omitempty"`
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
			d.Description = s.skillDescription(r.Context(), d.VersionID)
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
	case "file":
		s.handleSkillFile(w, r, id)
	case "export":
		s.handleSkillExport(w, r, id)
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

// runStatusGroups maps a runs-list filter to the statuses it covers. "failed" is every
// run that went wrong (a timeout is a failure too); "stopped" is a run that was ended or
// never started on purpose (stopped by hand, or skipped by budget / a paused task), which
// is not a fault. Any other value filters on that exact status, as before.
var runStatusGroups = map[string][]string{
	"running":   {"queued", "running"},
	"succeeded": {"succeeded"},
	"failed":    {"failed", "timeout"},
	"stopped":   {"canceled", "skipped"},
}

// runsFilter builds the WHERE part shared by the list and its counts: task, search text
// and (unless withStatus is false) the status filter. Everything goes in as a parameter.
func runsFilter(q url.Values, withStatus bool, loc *time.Location, now time.Time) (string, []any, error) {
	where := "WHERE 1=1"
	var args []any
	if from, to, ok, err := runsRange(q, loc, now); err != nil {
		return "", nil, err
	} else if ok {
		// created_at is RFC3339 in UTC, so the text compares in time order
		where += " AND r.created_at >= ? AND r.created_at < ?"
		args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	}
	if st := q.Get("status"); withStatus && st != "" {
		group, ok := runStatusGroups[st]
		if !ok {
			group = []string{st}
		}
		where += " AND r.status IN (?" + strings.Repeat(",?", len(group)-1) + ")"
		for _, g := range group {
			args = append(args, g)
		}
	}
	if tid := q.Get("task_id"); tid != "" {
		where += " AND r.task_id=?"
		args = append(args, tid)
	}
	if qs := strings.TrimSpace(q.Get("q")); qs != "" {
		if len(qs) > 100 {
			return "", nil, errors.New("search text too long")
		}
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(qs) + "%"
		where += ` AND (r.prompt LIKE ? ESCAPE '\' OR t.name LIKE ? ESCAPE '\' OR r.id LIKE ? ESCAPE '\' OR r.error_message LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like)
	}
	return where, args, nil
}

// runsRange turns the runs-list time filter into [from, to) instants. Days are calendar
// days in the scheduler's time zone (the zone every time on the page is shown in):
// range=today | yesterday | 7d | 30d (today and the days before it), or from=/to= as
// YYYY-MM-DD, both days included; either end may be left out. ok is false for no filter.
func runsRange(q url.Values, loc *time.Location, now time.Time) (from, to time.Time, ok bool, err error) {
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch rg := q.Get("range"); rg {
	case "", "all":
	case "today":
		return day0, day0.AddDate(0, 0, 1), true, nil
	case "yesterday":
		return day0.AddDate(0, 0, -1), day0, true, nil
	case "7d":
		return day0.AddDate(0, 0, -6), day0.AddDate(0, 0, 1), true, nil
	case "30d":
		return day0.AddDate(0, 0, -29), day0.AddDate(0, 0, 1), true, nil
	default:
		return from, to, false, errors.New("range must be today, yesterday, 7d or 30d")
	}
	fs, ts := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	if fs == "" && ts == "" {
		return from, to, false, nil
	}
	parse := func(name, v string) (time.Time, error) {
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return d, fmt.Errorf("%s must be a date like 2026-10-24", name)
		}
		return d, nil
	}
	from = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	to = day0.AddDate(0, 0, 1)
	if fs != "" {
		if from, err = parse("from", fs); err != nil {
			return
		}
	}
	if ts != "" {
		var d time.Time
		if d, err = parse("to", ts); err != nil {
			return
		}
		to = d.AddDate(0, 0, 1)
	}
	if !to.After(from) {
		return from, to, false, errors.New("from must not be after to")
	}
	return from, to, true, nil
}

// handleRuns lists runs, newest first by default. Paging: for the time sorts the next
// page starts after the last run shown (before=<run id>, a keyset cursor, so runs that
// start while you scroll don't shift the pages); the other sorts use offset.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where, args, err := runsFilter(q, true, s.loc(), time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// ids are time-ordered to the millisecond: they break ties between runs created in the
	// same second, so queued runs list in their real order.
	sortBy := q.Get("sort")
	order := s.runsOrder(sortBy)
	if order == "" {
		writeErr(w, 400, "sort must be new, old, cost, tokens or duration")
		return
	}
	if before := q.Get("before"); before != "" {
		if sortBy != "" && sortBy != "new" && sortBy != "old" {
			writeErr(w, 400, "before only works with sort=new or old; use offset")
			return
		}
		var created string
		if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT created_at FROM runs WHERE id=?`, before).Scan(&created); err != nil {
			writeErr(w, 400, "before: run not found")
			return
		}
		cmp := "<"
		if sortBy == "old" {
			cmp = ">"
		}
		where += " AND (r.created_at " + cmp + " ? OR (r.created_at = ? AND r.id " + cmp + " ?))"
		args = append(args, created, created, before)
	}
	where += " ORDER BY " + order + " LIMIT ?"
	args = append(args, limitParam(r, 50, 1000))
	if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		where += " OFFSET ?"
		args = append(args, off)
	}
	// With search text, read whole prompts so a hit past the 300-byte preview still shows
	// up in the row's excerpt; the prompt is cut to the preview afterwards.
	qs := strings.TrimSpace(q.Get("q"))
	promptMax := listPromptMax
	if qs != "" {
		promptMax = 0
	}
	runs, err := s.queryRunsOpt(r.Context(), promptMax, where, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if qs != "" {
		for i := range runs {
			runs[i].Match = findRunHit(runs[i], qs)
			runs[i].Prompt = textclip.Ellipsis(runs[i].Prompt, listPromptMax)
		}
	}
	writeJSON(w, 200, runs)
}

// runsOrder is the ORDER BY for the runs list's sort (new, old, cost, tokens, duration);
// "" for an unknown one.
func (s *Server) runsOrder(sortBy string) string {
	return map[string]string{"": "r.created_at DESC, r.id DESC", "new": "r.created_at DESC, r.id DESC", "old": "r.created_at ASC, r.id ASC",
		"cost": s.runAmt("r.") + " DESC, r.created_at DESC, r.id DESC", "tokens": "(COALESCE(r.input_tokens,0)+COALESCE(r.output_tokens,0)) DESC, r.created_at DESC, r.id DESC",
		"duration": "COALESCE(r.duration_ms,0) DESC, r.created_at DESC, r.id DESC"}[sortBy]
}

// handleRunCounts: GET /api/runs/counts — how many runs each filter chip would show,
// for the same task and search text as the list. One grouped query.
func (s *Server) handleRunCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	where, args, err := runsFilter(r.URL.Query(), false, s.loc(), time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// tasks are only needed to match the search text against the task name; without it
	// the count is read from idx_runs_status alone (44 ms -> 2 ms at 50 000 runs).
	join := ""
	if strings.TrimSpace(r.URL.Query().Get("q")) != "" {
		join = "LEFT JOIN tasks t ON t.id = r.task_id "
	}
	rows, err := s.deps.DB.QueryContext(r.Context(), `SELECT r.status, COUNT(1) FROM runs r `+join+where+` GROUP BY r.status`, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := map[string]int{"all": 0}
	for g := range runStatusGroups {
		out[g] = 0
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out["all"] += n
		for g, list := range runStatusGroups {
			for _, x := range list {
				if x == st {
					out[g] += n
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
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
		runs, err := s.queryRunsFull(r.Context(), "WHERE r.id=?", id)
		if err != nil || len(runs) == 0 {
			writeErr(w, 404, "run not found")
			return
		}
		// cost_detail: the calculation (tokens × price snapshot) for the run detail page
		// result: the agent's final answer (Markdown, capped) — only on the detail, never in lists
		var detail, result string
		var cut int
		_ = s.deps.DB.QueryRowContext(r.Context(), `SELECT cost_detail, result_text, result_truncated FROM runs WHERE id=?`, id).Scan(&detail, &result, &cut)
		out := struct {
			runDTO
			CostDetail      json.RawMessage `json:"cost_detail"`
			Currency        string          `json:"currency"`
			Result          string          `json:"result"`
			ResultTruncated bool            `json:"result_truncated,omitempty"`
		}{runs[0], nil, s.fx().Display, result, cut != 0}
		if json.Valid([]byte(detail)) {
			out.CostDetail = json.RawMessage(detail)
		}
		writeJSON(w, 200, out)
		return
	}
	if parts[1] == "rerun" && r.Method == http.MethodPost {
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.rerun(w, r, id) })).ServeHTTP(w, r)
		return
	}
	if parts[1] == "stop" && r.Method == http.MethodPost {
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.stopRun(w, r, id) })).ServeHTTP(w, r)
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
	// Lines are written as they are read and flushed once per batch, so a burst of
	// output is one network write rather than one per line.
	send := func(event, data string) {
		for _, line := range strings.Split(data, "\n") {
			if event != "" {
				fmt.Fprintf(w, "event: %s\n", event)
			} else {
				fmt.Fprintf(w, "id: %d\n", offset)
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
	}
	end := func(status string) {
		send("end", status)
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
			} else if path != "" {
				if raw, ok := runner.GzipRawSize(path + ".gz"); ok && n <= raw {
					offset = n
				}
			}
		}
	}
	// A finished run's log may have been gzipped (runner.CompressFinishedLogs): it is
	// complete, so it is read once from offset to the end and the stream closes.
	readPacked := func() bool {
		rc, packed, err := runner.OpenLogFile(path)
		if err != nil || !packed {
			if rc != nil {
				rc.Close()
			}
			return false
		}
		defer rc.Close()
		if _, err := io.CopyN(io.Discard, rc, offset); err != nil {
			return true
		}
		br := bufio.NewReader(rc)
		sent := false
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 {
				offset += int64(len(line))
				if line = strings.TrimRight(line, "\r\n"); line != "[[end]]" {
					send("", line)
					sent = true
				}
			}
			if err != nil {
				break
			}
		}
		if sent {
			flusher.Flush()
		}
		return true
	}
	readMore := func() bool {
		if path == "" {
			return false
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && readPacked() {
			return true
		}
		// Stat first (like Cronicle's fstat before read): nothing new means no open.
		if fi, err := os.Stat(path); err != nil || fi.Size() <= offset {
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
		ended, sent := false, false
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 && strings.HasSuffix(line, "\n") {
				offset += int64(len(line))
				line = strings.TrimRight(line, "\r\n")
				if line == "[[end]]" {
					ended = true
					continue
				}
				send("", line)
				sent = true
			}
			if err != nil {
				break
			}
		}
		if sent {
			flusher.Flush()
		}
		return ended
	}
	if readMore() {
		end(s.runStatus(ctx, runID))
		return
	}
	if path == "" && isTerminal(st) {
		send("", "(no log captured for this run)")
		end(st)
		return
	}
	// With LogWait the stream sleeps until the run writes (in-process push, like Uptime
	// Kuma emitting each heartbeat as it happens); the slow timer only catches a status
	// change without a final line and the idle cut-off. Without it, poll as before.
	poll := s.logPoll
	if s.deps.LogWait == nil || poll <= 0 {
		poll = 500 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var lastRead time.Time
	for {
		var bell <-chan struct{}
		done := func() {}
		if s.deps.LogWait != nil {
			bell, done = s.deps.LogWait(runID)
			// Output may have landed between the last read and taking the bell.
			if readMore() {
				done()
				end(s.runStatus(ctx, runID))
				return
			}
		}
		select {
		case <-ctx.Done():
			done()
			return
		case <-heartbeat.C:
			done()
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-bell:
			done()
			// Coalesce bursts: at most one read per logGap.
			if gap := s.logGap - time.Since(lastRead); gap > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(gap):
				}
			}
			lastRead = time.Now()
			if readMore() {
				end(s.runStatus(ctx, runID))
				return
			}
		case <-ticker.C:
			done()
			lastRead = time.Now()
			if readMore() {
				end(s.runStatus(ctx, runID))
				return
			}
			if cur := s.runStatus(ctx, runID); isTerminal(cur) {
				readMore()
				end(cur)
				return
			}
			if !s.sseAllowed() {
				send("idle", "stream paused (idle)")
				flusher.Flush()
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
	pwSet, _ := s.deps.Auth.HasPassword(r.Context())
	others := 0
	if sess := auth.SessionFromContext(r.Context()); sess != nil {
		others = s.deps.Auth.OtherSessions(r.Context(), sess.ID)
	}
	writeJSON(w, 200, map[string]any{
		"password_set":     pwSet,
		"other_sessions":   others,
		"webui_enabled":    s.Enabled(),
		"addr":             s.deps.Cfg.WebUIAddr,
		"allow_lan":        s.deps.Cfg.WebUIAllowLAN,
		"tls":              s.deps.Cfg.WebUITLSCert != "",
		"idle_minutes":     s.deps.Cfg.WebUIIdleMinutes,
		"providers":        providers,
		"default_provider": s.deps.Cfg.DefaultProvider,
		"budgets":          sum.Budgets,
		"fx":               s.fx(),
		"totp_enabled":     totp,
		"timezone":         s.loc().String(),
		"tz_offset":        tzOffsetMin(s.loc(), time.Now()),
		"version":          Version,
	})
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
	case "global", "task", "bucket":
	default:
		writeErr(w, 400, "bad scope")
		return
	}
	if body.Period != "day" && body.Period != "run" {
		writeErr(w, 400, "bad period")
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

var (
	reUSDCap   = regexp.MustCompile(`daily (USD|CNY) cap reached \(([\d.]+)/([\d.]+)\)`)
	reTokenCap = regexp.MustCompile(`daily token cap reached \((\d+)/(\d+)\)`)
	reUSDHeld  = regexp.MustCompile(`daily (USD|CNY) cap held by (\d+) running \(([\d.]+)\+([\d.]+)/([\d.]+)\)`)
)

// parseHeldDetail reads a "held by runs going" refusal: how much they hold and how many.
func parseHeldDetail(d string) (held, n string) {
	if m := reUSDHeld.FindStringSubmatch(d); m != nil {
		return curSym(m[1]) + m[4], m[2]
	}
	return "", ""
}

func curSym(c string) string {
	if c == "CNY" {
		return "¥"
	}
	return "$"
}

// parseBudgetDetail turns the stored English breach text into a code + numbers.
func parseBudgetDetail(d string) (code, used, limit string) {
	if m := reUSDCap.FindStringSubmatch(d); m != nil {
		sym := "$"
		if m[1] == "CNY" {
			sym = "¥"
		}
		return "usd_cap", sym + m[2], sym + m[3]
	}
	if m := reTokenCap.FindStringSubmatch(d); m != nil {
		return "token_cap", m[1], m[2]
	}
	if m := reUSDHeld.FindStringSubmatch(d); m != nil {
		return "held", curSym(m[1]) + m[3], curSym(m[1]) + m[5]
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
