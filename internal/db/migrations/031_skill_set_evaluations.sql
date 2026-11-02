-- Immutable package/version selection and recorded comparison outcomes survive restarts.
CREATE TABLE IF NOT EXISTS skill_set_evaluations (
  id TEXT PRIMARY KEY,
  set_id TEXT NOT NULL,
  version_id TEXT NOT NULL REFERENCES versions(id),
  status TEXT NOT NULL,
  state_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS skill_set_evaluations_by_set ON skill_set_evaluations(set_id,created_at DESC);
