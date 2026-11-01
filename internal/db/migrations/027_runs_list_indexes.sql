-- Runs list and Run dialog stay fast with a long history (tens of thousands of runs).
-- (created_at, id) is the list's whole sort key, so newest-first pages come straight off
-- the index instead of sorting every run; it replaces idx_runs_created (its prefix).
CREATE INDEX IF NOT EXISTS idx_runs_created_id ON runs(created_at, id);
DROP INDEX IF EXISTS idx_runs_created;
-- Status filters page newest first from the index (and the chip counts read it alone);
-- it replaces idx_runs_status (its prefix).
CREATE INDEX IF NOT EXISTS idx_runs_status_created ON runs(status, created_at, id);
DROP INDEX IF EXISTS idx_runs_status;
-- Small partial indexes: the Run dialog's last folder / recent folders read only quick
-- runs, and the recent prompts only runs without a task, newest first.
CREATE INDEX IF NOT EXISTS idx_runs_quick_created ON runs(created_at) WHERE trigger_kind = 'quick';
CREATE INDEX IF NOT EXISTS idx_runs_notask_created ON runs(created_at, id) WHERE task_id IS NULL OR task_id = '';
