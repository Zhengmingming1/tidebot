-- Phase 1: immutable versions, pointers, operation log, skills, requirement items.

CREATE TABLE IF NOT EXISTS blobs (
  sha256 TEXT PRIMARY KEY,
  size INTEGER NOT NULL,
  storage TEXT NOT NULL DEFAULT 'inline',
  data BLOB,
  refcount INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS versions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  version_no INTEGER NOT NULL,
  content_hash TEXT NOT NULL,
  parent_version_id TEXT REFERENCES versions(id),
  content_json TEXT,
  source TEXT NOT NULL,
  source_ref TEXT,
  message TEXT,
  archived INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  UNIQUE (entity_type, entity_id, version_no),
  UNIQUE (entity_type, entity_id, content_hash)
);

CREATE TABLE IF NOT EXISTS version_files (
  version_id TEXT NOT NULL REFERENCES versions(id),
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL REFERENCES blobs(sha256),
  mode INTEGER NOT NULL DEFAULT 420,
  PRIMARY KEY (version_id, path)
);

CREATE TABLE IF NOT EXISTS pointers (
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  name TEXT NOT NULL,
  version_id TEXT REFERENCES versions(id),
  updated_at TEXT NOT NULL,
  PRIMARY KEY (entity_type, entity_id, name)
);

CREATE TABLE IF NOT EXISTS version_tags (
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  tag TEXT NOT NULL,
  version_id TEXT NOT NULL REFERENCES versions(id),
  updated_at TEXT NOT NULL,
  PRIMARY KEY (entity_type, entity_id, tag)
);

CREATE TABLE IF NOT EXISTS operation_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL DEFAULT 'owner',
  at TEXT NOT NULL,
  batch_id TEXT,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  action TEXT NOT NULL,
  pointer TEXT,
  from_version_id TEXT,
  to_version_id TEXT,
  actor TEXT NOT NULL,
  reason TEXT,
  detail_json TEXT
);

CREATE TRIGGER IF NOT EXISTS oplog_no_update BEFORE UPDATE ON operation_log
BEGIN SELECT RAISE(ABORT, 'operation_log is append-only'); END;

CREATE TRIGGER IF NOT EXISTS oplog_no_delete BEFORE DELETE ON operation_log
BEGIN SELECT RAISE(ABORT, 'operation_log is append-only'); END;

CREATE INDEX IF NOT EXISTS idx_oplog_entity_time ON operation_log(entity_type, entity_id, at);
CREATE INDEX IF NOT EXISTS idx_versions_entity ON versions(entity_type, entity_id, version_no);

CREATE TABLE IF NOT EXISTS skills (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT 'owner',
  name TEXT NOT NULL UNIQUE,
  optimize_mode TEXT NOT NULL DEFAULT 'off',
  canary_ratio REAL NOT NULL DEFAULT 0.2,
  canary_runs INTEGER NOT NULL DEFAULT 10,
  trust_level TEXT NOT NULL DEFAULT 'local',
  last_analyzed_at TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS requirement_items (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  message_id TEXT,
  op TEXT NOT NULL,
  scope TEXT NOT NULL,
  target_spec_item TEXT,
  content TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  conflict_with TEXT,
  conflict_resolution TEXT,
  applied_version_id TEXT REFERENCES versions(id),
  consumed_run_id TEXT REFERENCES runs(id),
  consolidation_id TEXT,
  expires_at TEXT,
  created_at TEXT NOT NULL,
  decided_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_items_task_status ON requirement_items(task_id, status);
