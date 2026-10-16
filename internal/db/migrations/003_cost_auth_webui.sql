-- Phase 1b: cost control, single-user auth, notifications log.

CREATE TABLE IF NOT EXISTS budgets (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  scope_id TEXT NOT NULL DEFAULT '',
  period TEXT NOT NULL,
  max_usd REAL,
  max_tokens INTEGER,
  action TEXT NOT NULL DEFAULT 'pause_notify',
  warn_at_pct INTEGER NOT NULL DEFAULT 80,
  bucket TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(scope, scope_id, period, bucket)
);

CREATE TABLE IF NOT EXISTS cost_ledger (
  run_id TEXT PRIMARY KEY,
  bucket TEXT NOT NULL,
  task_id TEXT,
  usd REAL NOT NULL DEFAULT 0,
  tokens INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  day TEXT NOT NULL,
  month TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cost_day_bucket ON cost_ledger(day, bucket);
CREATE INDEX IF NOT EXISTS idx_cost_task_day ON cost_ledger(task_id, day);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY DEFAULT 'owner',
  password_hash TEXT NOT NULL DEFAULT '',
  totp_secret TEXT NOT NULL DEFAULT '',
  totp_enabled INTEGER NOT NULL DEFAULT 0,
  recovery_codes_hash TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  csrf_token TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  ip TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS login_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  ip TEXT NOT NULL,
  ok INTEGER NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY,
  event TEXT NOT NULL,
  ref TEXT,
  body TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'logged',
  created_at TEXT NOT NULL,
  delivered_at TEXT
);

CREATE TABLE IF NOT EXISTS budget_events (
  id TEXT PRIMARY KEY,
  at TEXT NOT NULL,
  scope TEXT NOT NULL,
  scope_id TEXT NOT NULL DEFAULT '',
  bucket TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  detail TEXT NOT NULL,
  action_taken TEXT NOT NULL DEFAULT ''
);
