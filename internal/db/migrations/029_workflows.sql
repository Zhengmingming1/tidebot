CREATE TABLE workflows (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL, deleted INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE workflow_versions (
  workflow_id TEXT NOT NULL REFERENCES workflows(id), version INTEGER NOT NULL,
  definition_json TEXT NOT NULL, created_at TEXT NOT NULL,
  PRIMARY KEY(workflow_id, version)
);
CREATE TABLE workflow_runs (
  id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL REFERENCES workflows(id),
  workflow_version INTEGER NOT NULL, status TEXT NOT NULL,
  input TEXT NOT NULL, provider TEXT NOT NULL DEFAULT '', snapshot_json TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE workflow_run_nodes (
  workflow_run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  node_id TEXT NOT NULL, status TEXT NOT NULL,
  child_run_id TEXT NOT NULL DEFAULT '', output TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(workflow_run_id, node_id)
);
CREATE INDEX idx_workflow_runs_created ON workflow_runs(created_at DESC, id);
CREATE INDEX idx_workflow_runs_workflow ON workflow_runs(workflow_id, created_at DESC);
