-- Phases 2–5: policies, canary, plugins, notifications, retention, backups, updates.

CREATE TABLE IF NOT EXISTS task_policies (
  task_id TEXT PRIMARY KEY REFERENCES tasks(id),
  catchup TEXT NOT NULL DEFAULT 'run_once',      -- skip | run_once | run_all
  catchup_grace_minutes INTEGER NOT NULL DEFAULT 360,
  retry_max INTEGER NOT NULL DEFAULT 2,
  priority TEXT NOT NULL DEFAULT 'normal',       -- urgent | normal | background
  allowed_tools TEXT NOT NULL DEFAULT '',        -- comma list, permission-rule syntax
  side_effects INTEGER NOT NULL DEFAULT 0,       -- 1 = never canary
  notify_success INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS skill_proposals (
  id TEXT PRIMARY KEY,
  skill_id TEXT NOT NULL REFERENCES skills(id),
  version_id TEXT NOT NULL REFERENCES versions(id),
  source TEXT NOT NULL,                 -- auto | user
  category TEXT NOT NULL DEFAULT '',
  rationale TEXT NOT NULL DEFAULT '',
  risk TEXT NOT NULL DEFAULT 'low',     -- low | medium | high
  checks_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'pending', -- pending | rejected | canary | promoted | discarded | withdrawn
  created_at TEXT NOT NULL,
  decided_at TEXT
);

CREATE TABLE IF NOT EXISTS canary_runs (
  run_id TEXT PRIMARY KEY,
  skill_id TEXT NOT NULL,
  version_id TEXT NOT NULL,
  is_candidate INTEGER NOT NULL,
  ok INTEGER NOT NULL,
  blocked INTEGER NOT NULL DEFAULT 0,   -- danger block or budget breach
  cost_usd REAL NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plugins (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  current_version_id TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  config_json TEXT NOT NULL DEFAULT '{}',
  installed_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plugin_versions (
  id TEXT PRIMARY KEY,
  plugin_id TEXT NOT NULL REFERENCES plugins(id),
  version TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  manifest_json TEXT NOT NULL,
  approved_permissions_json TEXT NOT NULL DEFAULT '',
  dir TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(plugin_id, content_hash)
);

CREATE TABLE IF NOT EXISTS plugin_kv (
  plugin_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (plugin_id, key)
);

CREATE TABLE IF NOT EXISTS trigger_bindings (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,              -- webhook | file_change | email | im
  plugin_id TEXT,
  task_id TEXT,
  button_id TEXT,
  config_json TEXT NOT NULL DEFAULT '{}',
  secret_hash TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notify_channels (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,              -- desktop | email | webhook | feishu | dingtalk | wecom | telegram | plugin
  name TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notify_rules (
  id TEXT PRIMARY KEY,
  event TEXT NOT NULL,             -- event kind or '*'
  task_id TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS run_flags (
  run_id TEXT PRIMARY KEY,
  important INTEGER NOT NULL DEFAULT 0,
  claude_version TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS backups (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  kind TEXT NOT NULL,              -- auto | manual
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pending_confirmations (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL DEFAULT '',
  task_id TEXT NOT NULL DEFAULT '',
  tool TEXT NOT NULL,
  input TEXT NOT NULL,
  rule TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | approved | denied | expired
  created_at TEXT NOT NULL,
  decided_at TEXT
);
