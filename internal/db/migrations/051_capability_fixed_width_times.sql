-- Capability timestamps used RFC3339Nano (trailing zeros dropped), and the
-- session-default events used RFC3339, so text ordering was wrong inside one
-- second ("…:05Z" sorted after "…:05.3Z"): version history, the event log and
-- a session's proposal cards could show a later row below an earlier one.
-- New rows use a fixed 9-digit fraction; pad old rows the same way. Only values
-- shaped like YYYY-MM-DDTHH:MM:SS[.f{1,8}]Z are touched; empty and other values
-- stay as they are. Versions and events are append-only: their guard triggers
-- are dropped for these updates and recreated unchanged in the same migration
-- transaction (only the text form of the same instant changes; lock_json and
-- lock_sha256 are not touched). capability_bindings is never ordered by time
-- and is fed from chat_sessions.updated_at by triggers, so it is left alone.
UPDATE capabilities SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE capabilities SET updated_at = substr(updated_at,1,19) || '.' || substr(CASE WHEN substr(updated_at,20,1)='.' THEN substr(updated_at,21,length(updated_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(updated_at) BETWEEN 20 AND 29 AND updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(updated_at)=20 OR (substr(updated_at,20,1)='.' AND substr(updated_at,21,length(updated_at)-21) NOT GLOB '*[^0-9]*'));
DROP TRIGGER IF EXISTS capability_versions_no_update;
DROP TRIGGER IF EXISTS capability_events_no_update;
UPDATE capability_versions SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE capability_events SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
CREATE TRIGGER capability_versions_no_update BEFORE UPDATE ON capability_versions
BEGIN SELECT RAISE(ABORT, 'capability versions are immutable'); END;
CREATE TRIGGER capability_events_no_update BEFORE UPDATE ON capability_events
BEGIN SELECT RAISE(ABORT, 'capability events are append-only'); END;
UPDATE capability_proposals SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE capability_proposals SET decided_at = substr(decided_at,1,19) || '.' || substr(CASE WHEN substr(decided_at,20,1)='.' THEN substr(decided_at,21,length(decided_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(decided_at) BETWEEN 20 AND 29 AND decided_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(decided_at)=20 OR (substr(decided_at,20,1)='.' AND substr(decided_at,21,length(decided_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE capability_proposals SET expires_at = substr(expires_at,1,19) || '.' || substr(CASE WHEN substr(expires_at,20,1)='.' THEN substr(expires_at,21,length(expires_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(expires_at) BETWEEN 20 AND 29 AND expires_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(expires_at)=20 OR (substr(expires_at,20,1)='.' AND substr(expires_at,21,length(expires_at)-21) NOT GLOB '*[^0-9]*'));
