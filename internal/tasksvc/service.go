// Package tasksvc provides task CRUD on top of immutable versions (D1/D3 basics).
package tasksvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	if spec.WorkspaceDir != "" {
		task.WorkspaceDir = spec.WorkspaceDir
	}
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
  overlap_policy, timeout_minutes, next_run_at, last_fired_at, created_at, updated_at
FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	var tasks []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Status, &t.Prompt, &t.WorkspaceDir,
			&t.ProviderID, &t.CronExpr, &t.OverlapPolicy, &t.TimeoutMinutes,
			&t.NextRunAt, &t.LastFiredAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
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

// SetStatus pauses or resumes a task.
func (s *Service) SetStatus(ctx context.Context, taskID, status, reason string) error {
	switch status {
	case "active", "paused", "archived":
	default:
		return fmt.Errorf("invalid status %q", status)
	}
	_, err := s.Store.DB.ExecContext(ctx, `
UPDATE tasks SET status=?, updated_at=? WHERE id=?`, status, nowUTC(), taskID)
	_ = reason
	return err
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
