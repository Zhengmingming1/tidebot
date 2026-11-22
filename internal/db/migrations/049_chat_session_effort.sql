-- The --effort value a session's turns use ('low' | 'medium' | 'high'; '' =
-- not passed, Claude Code decides). Before this, every turn took the effort of
-- the run the session continues, and a blank session could only use the
-- default. Existing sessions keep what they used: the origin run's value.
ALTER TABLE chat_sessions ADD COLUMN effort TEXT NOT NULL DEFAULT '';

UPDATE chat_sessions
SET effort = (SELECT r.effort FROM runs r WHERE r.id = chat_sessions.origin_run_id)
WHERE origin_run_id <> ''
  AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.id = chat_sessions.origin_run_id AND r.effort IN ('low', 'medium', 'high')
  );
