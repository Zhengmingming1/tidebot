package webui

import (
	"context"
	"database/sql"
	"github.com/Zhengmingming1/tidebot/internal/guard"

	"net/http"

	"strconv"

	"time"

	"github.com/Zhengmingming1/tidebot/internal/policy"

	"github.com/Zhengmingming1/tidebot/internal/scheduler"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/textclip"
)

// ---------- DTOs ----------

type taskDTO struct {
	CostControlMode    string `json:"cost_control_mode"`
	CostPolicyRevision int64  `json:"cost_policy_revision"`
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	Cron               string `json:"cron"`
	Prompt             string `json:"prompt"`
	Goal               string `json:"goal"`
	Provider           string `json:"provider"`
	Workspace          string `json:"workspace"`
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
	// Workflow: the workflow a workflow task runs (Name "" = deleted since).
	Workflow   *taskWorkflowForm `json:"workflow,omitempty"`
	RunsToday  int               `json:"runs_today"`
	LastStatus string            `json:"last_status"`
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
	// SkipRuns: upcoming fire times skipped by hand (RFC 3339 UTC, oldest first); the
	// schedule stays on and next_run_at is already the run after them.
	SkipRuns []string `json:"skip_runs,omitempty"`
}

func toTaskDTO(v tasksvc.TaskView) taskDTO {
	d := taskDTO{
		CostControlMode: v.Task.CostControlMode, CostPolicyRevision: v.Task.CostPolicyRevision, ID: v.Task.ID, Name: v.Task.Name, Status: v.Task.Status, Cron: v.Task.CronExpr,
		Prompt: v.Task.Prompt, Workspace: v.Task.WorkspaceDir,
		NextRunAt: v.Task.NextRunAt.String, LastFiredAt: v.Task.LastFiredAt.String,
		CreatedAt: v.Task.CreatedAt, UpdatedAt: v.Task.UpdatedAt,
		Constraints: []string{}, ConstraintIDs: []string{}, Skills: []string{}, Tags: []string{},
	}
	if v.Task.CronExpr != "" {
		d.SkipRuns = scheduler.FutureSkips(scheduler.ParseSkips(v.Task.SkipRuns), time.Now())
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
		if v.Spec.WorkflowID != "" {
			d.Workflow = &taskWorkflowForm{ID: v.Spec.WorkflowID, Version: v.Spec.WorkflowVersion}
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
	// Run dialog runs: the working folder, the skill (name and version) and
	// whether the run could change files ("read" | "edit"; "" otherwise).
	Workspace    string `json:"workspace,omitempty"`
	SkillID      string `json:"skill_id,omitempty"`
	SkillName    string `json:"skill_name,omitempty"`
	SkillVersion int    `json:"skill_version,omitempty"`
	SkillVerID   string `json:"skill_version_id,omitempty"`
	RunMode      string `json:"run_mode,omitempty"`
	// QueuePos is a queued run's place in line for a free slot (1 = next).
	QueuePos int `json:"queue_pos,omitempty"`
	// Match: with search text, which field matched and an excerpt around the hit.
	Match *runHit `json:"match,omitempty"`
	// Changes: how much an edit run changed in its folder (files, lines added/removed),
	// read from the recorded file list; nil when nothing was recorded (not an edit run,
	// not a git work tree, still running). The file list itself is only on the detail.
	Changes *runChangeSum `json:"changes,omitempty"`
}

// runChangeSum is the size of an edit run's changes, for list rows and exports.
type runChangeSum struct {
	Files int `json:"files"`
	Add   int `json:"add"`
	Del   int `json:"del"`
	// Undone: the changes were taken back out of the folder ("Undo changes").
	Undone bool `json:"undone,omitempty"`
	// UndoneN: files taken back one by one so far (the run not wholly undone).
	UndoneN int `json:"undone_n,omitempty"`
}

// runChangeCols reads runChangeSum from runs.file_changes in SQL, so lists never load the
// file list: -1 when there is none (or it isn't valid JSON).
const runChangeCols = `CASE WHEN r.run_mode IN ('edit','exec') AND r.file_changes<>'' AND json_valid(r.file_changes) THEN COALESCE(json_extract(r.file_changes,'$.count'),0) ELSE -1 END,
 CASE WHEN r.run_mode IN ('edit','exec') AND r.file_changes<>'' AND json_valid(r.file_changes) THEN COALESCE(json_extract(r.file_changes,'$.add'),0) ELSE 0 END,
 CASE WHEN r.run_mode IN ('edit','exec') AND r.file_changes<>'' AND json_valid(r.file_changes) THEN COALESCE(json_extract(r.file_changes,'$.del'),0) ELSE 0 END,
 r.changes_undone_at<>'',
 CASE WHEN r.changes_undone_files<>'' AND json_valid(r.changes_undone_files) THEN json_array_length(r.changes_undone_files) ELSE 0 END`

func (s *Server) runSelect() string {
	return `SELECT r.id, COALESCE(r.task_id,''), COALESCE(t.name,''), r.status, r.trigger_kind,
 COALESCE(r.provider_name,''), COALESCE(r.model,''), ` + s.runAmt("r.") + `,
 COALESCE(r.input_tokens,0), COALESCE(r.output_tokens,0), COALESCE(r.duration_ms,0),
 COALESCE(r.started_at,''), COALESCE(r.finished_at,''), r.created_at, COALESCE(r.error_message,''),
 r.prompt, COALESCE(v.version_no,0), COALESCE(r.cost_bucket,'production'),
 r.cost_amount, r.cost_currency, r.cost_source, r.cache_read_tokens, r.cache_write_tokens, r.requests,
 CASE WHEN r.run_mode<>'' THEN r.workspace_dir ELSE '' END, r.skill_id, COALESCE(sk.name,''), COALESCE(sv.version_no,0), r.skill_version_id, r.run_mode,
 ` + runChangeCols + `
FROM runs r LEFT JOIN tasks t ON t.id = r.task_id LEFT JOIN versions v ON v.id = r.task_version_id
 LEFT JOIN skills sk ON r.skill_id<>'' AND sk.id = r.skill_id LEFT JOIN versions sv ON r.skill_version_id<>'' AND sv.id = r.skill_version_id`
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
		if s.internalDir(d.Workspace) {
			d.Workspace = "" // a scratch folder of tidebot's (command runs without a folder)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// internalDir reports whether p is inside tidebot's own folders (a scratch folder).
func (s *Server) internalDir(p string) bool {
	if p == "" || s.deps.Cfg == nil {
		return false
	}
	for _, d := range []string{s.deps.Cfg.HomeDir, s.deps.Cfg.DataDir} {
		if d != "" && guard.Within(p, d) {
			return true
		}
	}
	return false
}

// scanRun reads one row of runSelect().
func scanRun(rows *sql.Rows) (runDTO, error) {
	var d runDTO
	var files, add, del int
	var undone bool
	var undoneN int
	err := rows.Scan(&d.ID, &d.TaskID, &d.TaskName, &d.Status, &d.Trigger, &d.Provider, &d.Model,
		&d.CostUSD, &d.InputTokens, &d.OutputTokens, &d.DurationMS, &d.StartedAt, &d.FinishedAt,
		&d.CreatedAt, &d.Error, &d.Prompt, &d.VersionNo, &d.Bucket,
		&d.CostAmount, &d.CostCurrency, &d.CostSource, &d.CacheRead, &d.CacheWrite, &d.Requests,
		&d.Workspace, &d.SkillID, &d.SkillName, &d.SkillVersion, &d.SkillVerID, &d.RunMode, &files, &add, &del, &undone, &undoneN)
	if err == nil && files >= 0 {
		d.Changes = &runChangeSum{Files: files, Add: add, Del: del, Undone: undone, UndoneN: undoneN}
	}
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
