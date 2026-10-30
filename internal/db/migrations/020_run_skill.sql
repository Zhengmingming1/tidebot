-- Runs started from the Run dialog with a skill and/or a working folder: which skill
-- (and the exact version) ran, and whether it could change files ('read' | 'edit';
-- '' for runs without a chosen folder). The folder is runs.workspace_dir.
ALTER TABLE runs ADD COLUMN skill_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN skill_version_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN run_mode TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_runs_skill_created ON runs(skill_id, created_at);
