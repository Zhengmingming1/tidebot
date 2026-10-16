-- Phase 0 minimal schema (subset of final-plan.md §20).
-- user_id reserved for future multi-user (D6).

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  user_id TEXT NOT NULL DEFAULT 'owner',
  key TEXT NOT NULL,
  value_json TEXT NOT NULL,
  updated_at TEXT,
  PRIMARY KEY (user_id, key)
);

CREATE TABLE IF NOT EXISTS secret_refs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  owner_type TEXT NOT NULL,
  owner_id TEXT,
  name TEXT NOT NULL,
  backend TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS providers (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  name TEXT NOT NULL UNIQUE,
  preset TEXT,
  base_url TEXT NOT NULL DEFAULT '',
  auth_env TEXT NOT NULL DEFAULT 'ANTHROPIC_AUTH_TOKEN',
  secret_ref_id TEXT REFERENCES secret_refs(id),
  model TEXT NOT NULL DEFAULT '',
  opus_model TEXT NOT NULL DEFAULT '',
  sonnet_model TEXT NOT NULL DEFAULT '',
  haiku_model TEXT NOT NULL DEFAULT '',
  fable_model TEXT NOT NULL DEFAULT '',
  small_fast_model TEXT NOT NULL DEFAULT '',
  subagent_model TEXT NOT NULL DEFAULT '',
  extra_env_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  prompt TEXT NOT NULL DEFAULT '',
  workspace_dir TEXT NOT NULL DEFAULT '',
  provider_id TEXT REFERENCES providers(id),
  cron_expr TEXT NOT NULL DEFAULT '',
  overlap_policy TEXT NOT NULL DEFAULT 'skip',
  timeout_minutes INTEGER NOT NULL DEFAULT 30,
  next_run_at TEXT,
  last_fired_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  task_id TEXT REFERENCES tasks(id),
  status TEXT NOT NULL DEFAULT 'queued',
  trigger_kind TEXT NOT NULL DEFAULT 'manual',
  provider_id TEXT,
  provider_name TEXT,
  model TEXT,
  prompt TEXT NOT NULL DEFAULT '',
  workspace_dir TEXT NOT NULL DEFAULT '',
  session_id TEXT,
  exit_code INTEGER,
  error_message TEXT,
  total_cost_usd REAL,
  input_tokens INTEGER,
  output_tokens INTEGER,
  duration_ms INTEGER,
  started_at TEXT,
  finished_at TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_task_created ON runs(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS idx_tasks_next_run ON tasks(status, next_run_at);
