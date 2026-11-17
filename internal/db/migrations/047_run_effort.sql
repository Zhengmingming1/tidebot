-- The --effort value used for this run ('low' | 'medium' | 'high'; '' = not
-- passed, Claude Code decides). Written with the run row, like run_mode, so
-- "save as task" can prefill it and a continued session can use the same value.
ALTER TABLE runs ADD COLUMN effort TEXT NOT NULL DEFAULT '';
