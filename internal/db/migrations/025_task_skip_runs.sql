-- "Skip next run": fire times of a scheduled task that should not run, as a JSON array of
-- RFC 3339 UTC strings ('' = none). The schedule stays active; next_run_at hops over them.
ALTER TABLE tasks ADD COLUMN skip_runs TEXT NOT NULL DEFAULT '';
