package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store wraps *sql.DB with typed helpers used by runner/scheduler/CLI.
type Store struct {
	DB *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// Task is a Phase-0 scheduled or on-demand job.
type Task struct {
	ID             string
	UserID         string
	Name           string
	Status         string
	Prompt         string
	WorkspaceDir   string
	ProviderID     sql.NullString
	CronExpr       string
	OverlapPolicy  string
	TimeoutMinutes int
	NextRunAt      sql.NullString
	LastFiredAt    sql.NullString
	CreatedAt      string
	UpdatedAt      string
}

// Run is one execution of a task (or ad-hoc prompt).
type Run struct {
	ID              string
	UserID          string
	TaskID          sql.NullString
	Status          string
	TriggerKind     string
	ProviderID      sql.NullString
	ProviderName    sql.NullString
	Model           sql.NullString
	Prompt          string
	WorkspaceDir    string
	SessionID       sql.NullString
	ExitCode        sql.NullInt64
	ErrorMessage    sql.NullString
	TotalCostUSD    sql.NullFloat64
	InputTokens     sql.NullInt64
	OutputTokens    sql.NullInt64
	DurationMS      sql.NullInt64
	StartedAt       sql.NullString
	FinishedAt      sql.NullString
	CreatedAt       string
	TaskVersionID   sql.NullString
	HasNextRunItems bool
}

// ProviderRow is a persisted provider profile.
type ProviderRow struct {
	ID              string
	UserID          string
	Name            string
	Preset          sql.NullString
	BaseURL         string
	AuthEnv         string
	SecretRefID     sql.NullString
	Model           string
	OpusModel       string
	SonnetModel     string
	HaikuModel      string
	FableModel      string
	SmallFastModel  string
	SubagentModel   string
	ExtraEnvJSON    string
	Enabled         bool
	CreatedAt       string
	UpdatedAt       string
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// InsertTask inserts a task row.
func (s *Store) InsertTask(ctx context.Context, t Task) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO tasks(id, user_id, name, status, prompt, workspace_dir, provider_id, cron_expr,
  overlap_policy, timeout_minutes, next_run_at, last_fired_at, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, nullDefault(t.UserID, "owner"), t.Name, nullDefault(t.Status, "active"),
		t.Prompt, t.WorkspaceDir, t.ProviderID, t.CronExpr, nullDefault(t.OverlapPolicy, "skip"),
		t.TimeoutMinutes, t.NextRunAt, t.LastFiredAt, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func nullDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// GetTask returns a task by id.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, user_id, name, status, prompt, workspace_dir, provider_id, cron_expr,
  overlap_policy, timeout_minutes, next_run_at, last_fired_at, created_at, updated_at
FROM tasks WHERE id = ?`, id)
	var t Task
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Status, &t.Prompt, &t.WorkspaceDir,
		&t.ProviderID, &t.CronExpr, &t.OverlapPolicy, &t.TimeoutMinutes,
		&t.NextRunAt, &t.LastFiredAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListDueTasks returns active tasks whose next_run_at <= now.
func (s *Store) ListDueTasks(ctx context.Context, now time.Time) ([]Task, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, user_id, name, status, prompt, workspace_dir, provider_id, cron_expr,
  overlap_policy, timeout_minutes, next_run_at, last_fired_at, created_at, updated_at
FROM tasks
WHERE status = 'active' AND cron_expr != '' AND next_run_at IS NOT NULL AND next_run_at <= ?
ORDER BY next_run_at ASC`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Status, &t.Prompt, &t.WorkspaceDir,
			&t.ProviderID, &t.CronExpr, &t.OverlapPolicy, &t.TimeoutMinutes,
			&t.NextRunAt, &t.LastFiredAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// HasActiveRun reports whether a task has a queued/running run (for overlap=skip).
func (s *Store) HasActiveRun(ctx context.Context, taskID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
SELECT COUNT(1) FROM runs WHERE task_id = ? AND status IN ('queued','running')`, taskID).Scan(&n)
	return n > 0, err
}

// UpdateTaskSchedule updates next/last fire timestamps.
func (s *Store) UpdateTaskSchedule(ctx context.Context, taskID string, lastFired, nextRun *time.Time) error {
	var last, next sql.NullString
	if lastFired != nil {
		last = sql.NullString{String: lastFired.UTC().Format(time.RFC3339), Valid: true}
	}
	if nextRun != nil {
		next = sql.NullString{String: nextRun.UTC().Format(time.RFC3339), Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
UPDATE tasks SET last_fired_at = ?, next_run_at = ?, updated_at = ? WHERE id = ?`,
		last, next, nowUTC(), taskID)
	return err
}

// InsertRun creates a run row.
func (s *Store) InsertRun(ctx context.Context, r Run) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO runs(id, user_id, task_id, status, trigger_kind, provider_id, provider_name, model,
  prompt, workspace_dir, session_id, exit_code, error_message, total_cost_usd, input_tokens,
  output_tokens, duration_ms, started_at, finished_at, created_at, task_version_id, has_next_run_items)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, nullDefault(r.UserID, "owner"), r.TaskID, nullDefault(r.Status, "queued"),
		nullDefault(r.TriggerKind, "manual"), r.ProviderID, r.ProviderName, r.Model,
		r.Prompt, r.WorkspaceDir, r.SessionID, r.ExitCode, r.ErrorMessage, r.TotalCostUSD,
		r.InputTokens, r.OutputTokens, r.DurationMS, r.StartedAt, r.FinishedAt, r.CreatedAt,
		r.TaskVersionID, boolToInt(r.HasNextRunItems),
	)
	return err
}

// UpdateRunFinish persists terminal run state and usage.
func (s *Store) UpdateRunFinish(ctx context.Context, r Run) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE runs SET status=?, session_id=?, exit_code=?, error_message=?, total_cost_usd=?,
  input_tokens=?, output_tokens=?, duration_ms=?, started_at=?, finished_at=?,
  provider_id=?, provider_name=?, model=?
WHERE id=?`,
		r.Status, r.SessionID, r.ExitCode, r.ErrorMessage, r.TotalCostUSD,
		r.InputTokens, r.OutputTokens, r.DurationMS, r.StartedAt, r.FinishedAt,
		r.ProviderID, r.ProviderName, r.Model, r.ID,
	)
	return err
}

// MarkRunRunning sets status=running and started_at.
func (s *Store) MarkRunRunning(ctx context.Context, runID string, startedAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET status='running', started_at=? WHERE id=?`,
		startedAt.UTC().Format(time.RFC3339), runID)
	return err
}

// ListRuns returns recent runs, optionally filtered by task.
func (s *Store) ListRuns(ctx context.Context, taskID string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows *sql.Rows
	var err error
	if taskID == "" {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, user_id, task_id, status, trigger_kind, provider_id, provider_name, model,
  prompt, workspace_dir, session_id, exit_code, error_message, total_cost_usd, input_tokens,
  output_tokens, duration_ms, started_at, finished_at, created_at
FROM runs ORDER BY created_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.DB.QueryContext(ctx, `
SELECT id, user_id, task_id, status, trigger_kind, provider_id, provider_name, model,
  prompt, workspace_dir, session_id, exit_code, error_message, total_cost_usd, input_tokens,
  output_tokens, duration_ms, started_at, finished_at, created_at
FROM runs WHERE task_id = ? ORDER BY created_at DESC LIMIT ?`, taskID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.UserID, &r.TaskID, &r.Status, &r.TriggerKind,
			&r.ProviderID, &r.ProviderName, &r.Model, &r.Prompt, &r.WorkspaceDir,
			&r.SessionID, &r.ExitCode, &r.ErrorMessage, &r.TotalCostUSD, &r.InputTokens,
			&r.OutputTokens, &r.DurationMS, &r.StartedAt, &r.FinishedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertProvider inserts or updates a provider profile.
func (s *Store) UpsertProvider(ctx context.Context, p ProviderRow) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO providers(id, user_id, name, preset, base_url, auth_env, secret_ref_id, model,
  opus_model, sonnet_model, haiku_model, fable_model, small_fast_model, subagent_model,
  extra_env_json, enabled, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET
  preset=excluded.preset, base_url=excluded.base_url, auth_env=excluded.auth_env,
  secret_ref_id=excluded.secret_ref_id, model=excluded.model, opus_model=excluded.opus_model,
  sonnet_model=excluded.sonnet_model, haiku_model=excluded.haiku_model,
  fable_model=excluded.fable_model, small_fast_model=excluded.small_fast_model,
  subagent_model=excluded.subagent_model, extra_env_json=excluded.extra_env_json,
  enabled=excluded.enabled, updated_at=excluded.updated_at`,
		p.ID, nullDefault(p.UserID, "owner"), p.Name, p.Preset, p.BaseURL, p.AuthEnv, p.SecretRefID,
		p.Model, p.OpusModel, p.SonnetModel, p.HaikuModel, p.FableModel, p.SmallFastModel,
		p.SubagentModel, nullDefault(p.ExtraEnvJSON, "{}"), boolToInt(p.Enabled), p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListProviders returns all providers.
func (s *Store) ListProviders(ctx context.Context) ([]ProviderRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, user_id, name, preset, base_url, auth_env, secret_ref_id, model,
  opus_model, sonnet_model, haiku_model, fable_model, small_fast_model, subagent_model,
  extra_env_json, enabled, created_at, updated_at
FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderRow
	for rows.Next() {
		var p ProviderRow
		var en int
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Preset, &p.BaseURL, &p.AuthEnv, &p.SecretRefID,
			&p.Model, &p.OpusModel, &p.SonnetModel, &p.HaikuModel, &p.FableModel, &p.SmallFastModel,
			&p.SubagentModel, &p.ExtraEnvJSON, &en, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Enabled = en != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProviderByName looks up a provider profile.
func (s *Store) GetProviderByName(ctx context.Context, name string) (*ProviderRow, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, user_id, name, preset, base_url, auth_env, secret_ref_id, model,
  opus_model, sonnet_model, haiku_model, fable_model, small_fast_model, subagent_model,
  extra_env_json, enabled, created_at, updated_at
FROM providers WHERE name = ?`, name)
	var p ProviderRow
	var en int
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Preset, &p.BaseURL, &p.AuthEnv, &p.SecretRefID,
		&p.Model, &p.OpusModel, &p.SonnetModel, &p.HaikuModel, &p.FableModel, &p.SmallFastModel,
		&p.SubagentModel, &p.ExtraEnvJSON, &en, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Enabled = en != 0
	return &p, nil
}

// GetProviderByID looks up by primary key.
func (s *Store) GetProviderByID(ctx context.Context, id string) (*ProviderRow, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, user_id, name, preset, base_url, auth_env, secret_ref_id, model,
  opus_model, sonnet_model, haiku_model, fable_model, small_fast_model, subagent_model,
  extra_env_json, enabled, created_at, updated_at
FROM providers WHERE id = ?`, id)
	var p ProviderRow
	var en int
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Preset, &p.BaseURL, &p.AuthEnv, &p.SecretRefID,
		&p.Model, &p.OpusModel, &p.SonnetModel, &p.HaikuModel, &p.FableModel, &p.SmallFastModel,
		&p.SubagentModel, &p.ExtraEnvJSON, &en, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Enabled = en != 0
	return &p, nil
}

// CountActiveRuns returns number of currently running/queued runs.
func (s *Store) CountActiveRuns(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs WHERE status IN ('queued','running')`).Scan(&n)
	return n, err
}

// ErrNotFound is returned when a row is missing.
var ErrNotFound = fmt.Errorf("not found")
