-- Runs list: newest-first order and the time-range filter without a full scan.
CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at);
