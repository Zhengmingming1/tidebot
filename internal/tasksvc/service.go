// Package tasksvc provides task CRUD on top of immutable versions (D1/D3 basics).
package tasksvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Service manages tasks and their versioned specs.
type Service struct {
	Store *db.Store
	Ver   *version.Service
	Sched *scheduler.Scheduler // optional; used to compute next_run_at
}

func New(store *db.Store, ver *version.Service, sched *scheduler.Scheduler) *Service {
	return &Service{Store: store, Ver: ver, Sched: sched}
}

// CreateInput creates a task with initial v1.
type CreateInput struct {
	Name           string `json:"name"`
	Prompt         string `json:"prompt"`
	Goal           string `json:"goal"`
	WorkspaceDir   string `json:"workspace_dir"`
	ProviderName   string `json:"provider_name"`
	CronExpr       string `json:"cron_expr"`
	OverlapPolicy  string `json:"overlap_policy"`
	TimeoutMinutes int    `json:"timeout_minutes"`
	Actor          string `json:"actor"`
}

// TaskView is task row + current version summary.
type TaskView struct {
	Task           db.Task          `json:"task"`
	CurrentVersion *version.Version `json:"current_version,omitempty"`
	Spec           *Spec            `json:"spec,omitempty"`
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// Create inserts task + v1 + current pointer.
func (s *Service) Create(ctx context.Context, in CreateInput) (TaskView, error) {
	if in.Name == "" || in.Prompt == "" {
		return TaskView{}, fmt.Errorf("name and prompt required")
	}
	if in.Actor == "" {
		in.Actor = "user"
	}
	if in.TimeoutMinutes <= 0 {
		in.TimeoutMinutes = 30
	}
	if in.OverlapPolicy == "" {
		in.OverlapPolicy = "skip"
	}
	if in.Goal == "" {
		in.Goal = in.Prompt
	}
	tid := id.New("task")
	now := nowUTC()
	spec := Spec{
		Goal: in.Goal, Prompt: in.Prompt, Provider: in.ProviderName,
		WorkspaceDir: in.WorkspaceDir, TimeoutMinutes: in.TimeoutMinutes,
	}
	var next sql.NullString
	if in.CronExpr != "" && s.Sched != nil {
		t, err := s.Sched.ComputeInitialNext(in.CronExpr)
		if err != nil {
			return TaskView{}, fmt.Errorf("cron: %w", err)
		}
		next = sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
	}
	task := db.Task{
		ID: tid, Name: in.Name, Status: "active", Prompt: in.Prompt,
		WorkspaceDir: in.WorkspaceDir, CronExpr: in.CronExpr,
		OverlapPolicy: in.OverlapPolicy, TimeoutMinutes: in.TimeoutMinutes,
		NextRunAt: next, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.InsertTask(ctx, task); err != nil {
		return TaskView{}, err
	}
	res, err := s.Ver.Commit(ctx, version.CommitInput{
		EntityType: version.EntityTask, EntityID: tid,
		Content: spec, Source: "user_edit", Actor: in.Actor, Reason: "create task", Message: "v1",
	})
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: task, CurrentVersion: res.Version, Spec: &spec}, nil
}

// UpdateSpec commits a new task version from an edited spec (pointer move).
func (s *Service) UpdateSpec(ctx context.Context, taskID string, spec Spec, actor, reason string) (TaskView, error) {
	task, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	if actor == "" {
		actor = "user"
	}
	res, err := s.Ver.Commit(ctx, version.CommitInput{
		EntityType: version.EntityTask, EntityID: taskID,
		Content: spec, Source: "user_edit", Actor: actor, Reason: reason,
	})
	if err != nil {
		return TaskView{}, err
	}
	// Keep denormalized prompt/workspace in sync for scheduler/runner.
	task.Prompt = spec.Prompt
	task.WorkspaceDir = spec.WorkspaceDir // "" = per-task default, as in Rollback
	if spec.TimeoutMinutes > 0 {
		task.TimeoutMinutes = spec.TimeoutMinutes
	}
	task.UpdatedAt = nowUTC()
	if _, err := s.Store.DB.ExecContext(ctx, `
UPDATE tasks SET prompt=?, workspace_dir=?, timeout_minutes=?, updated_at=? WHERE id=?`,
		task.Prompt, task.WorkspaceDir, task.TimeoutMinutes, task.UpdatedAt, taskID); err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: *task, CurrentVersion: res.Version, Spec: &spec}, nil
}

// Get returns task + current version/spec.
func (s *Service) Get(ctx context.Context, taskID string) (TaskView, error) {
	task, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	view := TaskView{Task: *task}
	ver, err := s.Ver.CurrentVersion(ctx, version.EntityTask, taskID)
	if err == sql.ErrNoRows {
		return view, nil
	}
	if err != nil {
		return TaskView{}, err
	}
	view.CurrentVersion = ver
	var spec Spec
	if err := json.Unmarshal([]byte(ver.ContentJSON), &spec); err == nil {
		view.Spec = &spec
	}
	return view, nil
}

// List returns all tasks (phase1: no pagination).
// Note: with MaxOpenConns(1) we must finish scanning rows before nested queries.
func (s *Service) List(ctx context.Context) ([]TaskView, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `
SELECT id, user_id, name, status, prompt, workspace_dir, provider_id, cron_expr,
  overlap_policy, timeout_minutes, next_run_at, last_fired_at, created_at, updated_at,
  pause_kind, pause_detail, paused_at
FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	var tasks []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Status, &t.Prompt, &t.WorkspaceDir,
			&t.ProviderID, &t.CronExpr, &t.OverlapPolicy, &t.TimeoutMinutes,
			&t.NextRunAt, &t.LastFiredAt, &t.CreatedAt, &t.UpdatedAt,
			&t.PauseKind, &t.PauseDetail, &t.PausedAt); err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var out []TaskView
	for _, t := range tasks {
		view := TaskView{Task: t}
		if ver, err := s.Ver.CurrentVersion(ctx, version.EntityTask, t.ID); err == nil {
			view.CurrentVersion = ver
			var spec Spec
			if json.Unmarshal([]byte(ver.ContentJSON), &spec) == nil {
				view.Spec = &spec
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// Why a task is paused (tasks.pause_kind).
const (
	PauseUser    = "user"    // by hand (WebUI, agentctl)
	PauseBreaker = "breaker" // consecutive failures (detail: error class)
	PauseBudget  = "budget"  // a budget cap was hit (detail: the budget reason)
	PausePack    = "pack"    // imported from a pack, waiting for review
	PausePlugin  = "plugin"  // its trigger plugin was uninstalled (detail: plugin id)
	// PauseSnooze: paused by hand until a time (detail: the end, RFC3339 UTC). It resumes by
	// itself then (WakeSnoozed), and the fire times that fell inside it are skipped.
	PauseSnooze = "snooze"
)

// SetStatus pauses (by hand) or resumes / archives a task; see SetStatusWhy.
func (s *Service) SetStatus(ctx context.Context, taskID, status, reason string) error {
	_ = reason
	return s.SetStatusWhy(ctx, taskID, status, PauseUser, "")
}

// Pause pauses a task and records why. A task that is already paused keeps its
// first reason (a breaker trip after a manual pause does not relabel it).
func (s *Service) Pause(ctx context.Context, taskID, kind, detail string) error {
	return s.SetStatusWhy(ctx, taskID, "paused", kind, detail)
}

// SetStatusWhy changes the status. Going to paused records kind / detail / time unless
// the task was paused already; leaving paused clears them. Resuming a paused task also
// clears its consecutive-failure count, so a task the breaker stopped gets the full
// number of tries again instead of being paused by its very next failure.
func (s *Service) SetStatusWhy(ctx context.Context, taskID, status, kind, detail string) error {
	switch status {
	case "active", "paused", "archived":
	default:
		return fmt.Errorf("invalid status %q", status)
	}
	for len(detail) > 300 { // keep it short and valid UTF-8
		_, n := utf8.DecodeLastRuneInString(detail)
		detail = detail[:len(detail)-n]
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old, oldKind, cronExpr string
	var next sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status, pause_kind, cron_expr, next_run_at FROM tasks WHERE id=?`, taskID).Scan(&old, &oldKind, &cronExpr, &next); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // nothing to change (same as the plain UPDATE before)
		}
		return err
	}
	now := nowUTC()
	switch {
	case status == "paused" && old == "paused":
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, now, taskID)
	case status == "paused":
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET status='paused', pause_kind=?, pause_detail=?, paused_at=?, updated_at=? WHERE id=?`,
			kind, detail, now, now, taskID)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET status=?, pause_kind='', pause_detail='', paused_at=NULL, updated_at=? WHERE id=?`,
			status, now, taskID)
	}
	if err != nil {
		return err
	}
	if status == "active" && old == "paused" {
		if _, err := tx.ExecContext(ctx, `UPDATE task_policies SET consecutive_failures=0 WHERE task_id=?`, taskID); err != nil {
			return err
		}
		if oldKind == PauseSnooze {
			if err := s.skipSnoozed(ctx, tx, taskID, cronExpr, next, time.Now()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// skipSnoozed moves a waking task's next fire time past now: a snooze means "not during
// this time", so its missed fire times are skipped instead of caught up (the way Uptime
// Kuma records checks inside a monitor's maintenance window as maintenance, not as runs
// owed later). A fire time still ahead is kept.
func (s *Service) skipSnoozed(ctx context.Context, tx *sql.Tx, taskID, cronExpr string, next sql.NullString, now time.Time) error {
	if cronExpr == "" || !next.Valid || s.Sched == nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, next.String); err == nil && t.After(now) {
		return nil
	}
	nx, err := s.Sched.NextRun(cronExpr, now)
	if err != nil {
		return nil // a broken expression is reported where it is edited
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET next_run_at=? WHERE id=?`, nx.UTC().Format(time.RFC3339), taskID)
	return err
}

// ErrPausedOther: the task is paused for a reason the user has to look at first
// (failures, budget, a removed plugin, an import waiting for review).
var ErrPausedOther = errors.New("task is paused for another reason; resume it first")

// PauseFor pauses a task by hand until `until` (zero = until resumed by hand). An active
// task, a task paused by hand and a snoozed task can be paused this way (pausing again
// replaces the end and keeps when the pause started); an automatic pause is left alone.
func (s *Service) PauseFor(ctx context.Context, taskID string, until time.Time) error {
	kind, detail := PauseUser, ""
	if !until.IsZero() {
		if !until.After(time.Now()) {
			return errors.New("the end must be in the future")
		}
		kind, detail = PauseSnooze, until.UTC().Format(time.RFC3339)
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var st, oldKind string
	if err := tx.QueryRowContext(ctx, `SELECT status, pause_kind FROM tasks WHERE id=?`, taskID).Scan(&st, &oldKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task not found")
		}
		return err
	}
	now := nowUTC()
	switch {
	case st == "active":
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET status='paused', pause_kind=?, pause_detail=?, paused_at=?, updated_at=? WHERE id=?`, kind, detail, now, now, taskID)
	case st == "paused" && (oldKind == PauseUser || oldKind == PauseSnooze || oldKind == ""):
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET pause_kind=?, pause_detail=?, paused_at=COALESCE(paused_at, ?), updated_at=? WHERE id=?`, kind, detail, now, now, taskID)
	case st == "paused":
		return ErrPausedOther
	default:
		return fmt.Errorf("task not found")
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// WakeSnoozed resumes every task whose snooze has ended by now and returns their ids.
// Called from the scheduler's poll, so a snooze that ran out while tidebot was off ends
// on the first tick after start-up.
func (s *Service) WakeSnoozed(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM tasks WHERE status='paused' AND pause_kind=? AND pause_detail <= ?`,
		PauseSnooze, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var woke []string
	for _, id := range ids {
		if err := s.SetStatusWhy(ctx, id, "active", "", ""); err != nil {
			return woke, err
		}
		woke = append(woke, id)
	}
	return woke, nil
}

// Rollback moves current pointer to versionID (no new version).
func (s *Service) Rollback(ctx context.Context, taskID, versionID, actor, reason string) error {
	ver, err := s.Ver.GetVersion(ctx, versionID)
	if err != nil {
		return err
	}
	if ver.EntityType != version.EntityTask || ver.EntityID != taskID {
		return fmt.Errorf("version does not belong to task")
	}
	if actor == "" {
		actor = "user"
	}
	if reason == "" {
		reason = "rollback"
	}
	if err := s.Ver.MovePointer(ctx, version.EntityTask, taskID, version.PointerCurrent, versionID, actor, reason, ""); err != nil {
		return err
	}
	// sync denormalized fields
	var spec Spec
	if err := json.Unmarshal([]byte(ver.ContentJSON), &spec); err == nil {
		_, _ = s.Store.DB.ExecContext(ctx, `
UPDATE tasks SET prompt=?, workspace_dir=?, timeout_minutes=?, updated_at=? WHERE id=?`,
			spec.Prompt, spec.WorkspaceDir, spec.TimeoutMinutes, nowUTC(), taskID)
	}
	return nil
}

// Tag sets a version tag.
func (s *Service) Tag(ctx context.Context, taskID, tag, versionID, actor string) error {
	return s.Ver.SetTag(ctx, version.EntityTask, taskID, tag, versionID, actor, "tag")
}

// CompilePrompt builds the run prompt from current spec + scheduled next_run amendments.
func (s *Service) CompilePrompt(ctx context.Context, taskID string) (prompt string, versionID string, nextRunItemIDs []string, err error) {
	view, err := s.Get(ctx, taskID)
	if err != nil {
		return "", "", nil, err
	}
	if view.CurrentVersion == nil || view.Spec == nil {
		return view.Task.Prompt, "", nil, nil
	}
	versionID = view.CurrentVersion.ID
	var b strings.Builder
	b.WriteString(view.Spec.Prompt)
	if view.Spec.Goal != "" && view.Spec.Goal != view.Spec.Prompt {
		b.WriteString("\n\n# Goal\n")
		b.WriteString(view.Spec.Goal)
	}
	if len(view.Spec.Constraints) > 0 {
		b.WriteString("\n\n# Constraints\n")
		for _, c := range view.Spec.Constraints {
			b.WriteString("- [")
			b.WriteString(c.ID)
			b.WriteString("] ")
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
	}
	rows, err := s.Store.DB.QueryContext(ctx, `
SELECT id, content FROM requirement_items
WHERE task_id=? AND status='scheduled' AND scope='next_run'
ORDER BY created_at`, taskID)
	if err != nil {
		return "", "", nil, err
	}
	defer rows.Close()
	var extras []string
	for rows.Next() {
		var iid, content string
		if err := rows.Scan(&iid, &content); err != nil {
			return "", "", nil, err
		}
		nextRunItemIDs = append(nextRunItemIDs, iid)
		extras = append(extras, content)
	}
	if len(extras) > 0 {
		b.WriteString("\n\n# One-time amendments (this run only)\n")
		for _, e := range extras {
			b.WriteString("- ")
			b.WriteString(e)
			b.WriteByte('\n')
		}
	}
	return b.String(), versionID, nextRunItemIDs, nil
}

// SetSchedule changes the cron expression ("" = manual only) and recomputes next_run_at.
func (s *Service) SetSchedule(ctx context.Context, taskID, cronExpr string) error {
	var next sql.NullString
	if cronExpr != "" {
		if s.Sched == nil {
			return fmt.Errorf("scheduler unavailable")
		}
		t, err := s.Sched.ComputeInitialNext(cronExpr)
		if err != nil {
			return fmt.Errorf("cron: %w", err)
		}
		next = sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
	}
	res, err := s.Store.DB.ExecContext(ctx, `UPDATE tasks SET cron_expr=?, next_run_at=?, updated_at=? WHERE id=?`, cronExpr, next, nowUTC(), taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

// Rename changes the display name.
func (s *Service) Rename(ctx context.Context, taskID, name string) error {
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE tasks SET name=?, updated_at=? WHERE id=?`, name, nowUTC(), taskID)
	return err
}

// Duplicate copies a task's current spec, schedule and settings into a new task.
// The copy starts paused so a scheduled task never fires twice by accident, and gets
// a fresh v1 (no version history, runs or triggers carry over). name must be unique
// among live tasks; UniqueName helps pick one.
func (s *Service) Duplicate(ctx context.Context, srcID, name, actor string) (TaskView, error) {
	src, err := s.Get(ctx, srcID)
	if err != nil || src.Spec == nil || src.Task.Status == "archived" {
		return TaskView{}, fmt.Errorf("task not found")
	}
	if name == "" {
		return TaskView{}, fmt.Errorf("name required")
	}
	if actor == "" {
		actor = "user"
	}
	spec := *src.Spec
	spec.Constraints = append([]SpecConstraint(nil), spec.Constraints...)
	spec.SkillBindings = append([]SkillBinding(nil), spec.SkillBindings...)
	spec.AllowedTools = append([]string(nil), spec.AllowedTools...)
	var next sql.NullString
	if src.Task.CronExpr != "" && s.Sched != nil {
		if t, err := s.Sched.ComputeInitialNext(src.Task.CronExpr); err == nil {
			next = sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
		}
	}
	now := nowUTC()
	task := db.Task{
		ID: id.New("task"), UserID: src.Task.UserID, Name: name, Status: "paused", Prompt: spec.Prompt,
		WorkspaceDir: src.Task.WorkspaceDir, ProviderID: src.Task.ProviderID, CronExpr: src.Task.CronExpr,
		OverlapPolicy: src.Task.OverlapPolicy, TimeoutMinutes: src.Task.TimeoutMinutes,
		NextRunAt: next, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.InsertTask(ctx, task); err != nil {
		return TaskView{}, err
	}
	res, err := s.Ver.Commit(ctx, version.CommitInput{
		EntityType: version.EntityTask, EntityID: task.ID,
		Content: spec, Source: "user_edit", Actor: actor, Reason: "duplicate of " + src.Task.ID, Message: "v1",
	})
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: task, CurrentVersion: res.Version, Spec: &spec}, nil
}

// UniqueName returns base, or base followed by " 2", " 3", … when a live task already
// uses it. The result is cut to max runes (base shortened first, the number kept).
func (s *Service) UniqueName(ctx context.Context, base string, max int) (string, error) {
	base = strings.TrimSpace(base)
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT name FROM tasks WHERE status != 'archived'`)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return "", err
		}
		taken[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	fit := func(b, suffix string) string {
		r := []rune(b)
		if keep := max - utf8.RuneCountInString(suffix); len(r) > keep {
			if keep < 0 {
				keep = 0
			}
			r = r[:keep]
		}
		return strings.TrimSpace(string(r)) + suffix
	}
	for i := 1; i < 1000; i++ {
		suffix := ""
		if i > 1 {
			suffix = " " + strconv.Itoa(i)
		}
		if n := fit(base, suffix); !taken[n] {
			return n, nil
		}
	}
	return "", fmt.Errorf("no free name")
}
