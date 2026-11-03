CREATE TABLE skill_repositories (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  ref TEXT NOT NULL DEFAULT '',
  root TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL DEFAULT 1,
  commit_id TEXT NOT NULL DEFAULT '',
  candidates_json TEXT NOT NULL DEFAULT '[]',
  last_scanned_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(url, ref, root)
);

CREATE TABLE skill_repository_bindings (
  skill_id TEXT PRIMARY KEY REFERENCES skills(id),
  repository_id TEXT REFERENCES skill_repositories(id) ON DELETE SET NULL,
  repository_url TEXT NOT NULL,
  ref TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL,
  commit_id TEXT NOT NULL,
  tree_hash TEXT NOT NULL DEFAULT '',
  base_version_id TEXT NOT NULL REFERENCES versions(id),
  latest_version_id TEXT NOT NULL REFERENCES versions(id),
  fetched_at TEXT NOT NULL,
  UNIQUE(repository_url, ref, path)
);
CREATE INDEX skill_repository_bindings_repository ON skill_repository_bindings(repository_id);
