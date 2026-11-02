CREATE TABLE skill_sets (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 deleted INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE run_skill_bindings (
 run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 skill_id TEXT NOT NULL,
 version_id TEXT NOT NULL,
 skill_name TEXT NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('primary','auxiliary')),
 position INTEGER NOT NULL,
 PRIMARY KEY(run_id,skill_id)
);
CREATE INDEX idx_run_skill_bindings_skill ON run_skill_bindings(skill_id,run_id);
