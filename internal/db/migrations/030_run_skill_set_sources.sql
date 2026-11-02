CREATE TABLE run_skill_set_sources (
 run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL,
 version_id TEXT NOT NULL,
 source_state TEXT NOT NULL CHECK(source_state IN ('linked', 'modified', 'missing'))
);
CREATE INDEX idx_run_skill_set_sources_set ON run_skill_set_sources(set_id, run_id);
