-- The chat page checks a session's cards on every poll. Covering the columns
-- it compares lets SQLite answer from the index without walking the large
-- payload_json overflow pages.
DROP INDEX IF EXISTS capability_proposals_session;
CREATE INDEX capability_proposals_session ON capability_proposals(session_id,created_at,id,status,decided_at);
