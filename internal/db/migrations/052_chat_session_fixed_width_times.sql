-- Session times used RFC3339 (whole seconds), but installing a capability in
-- a session stamps chat_sessions.updated_at with the fixed 9-digit layout (051),
-- so the session list (ORDER BY updated_at DESC) was wrong inside one second:
-- "…:05Z" sorted above a later "…:05.4…Z" ('Z' > '.'), and two sessions touched
-- in the same second fell back to id (creation) order. New rows use the fixed
-- layout; pad old rows the same way. Only values shaped like
-- YYYY-MM-DDTHH:MM:SS[.f{1,8}]Z are touched; empty and other values stay.
-- The capability triggers on chat_sessions fire on bindings_json, not these.
UPDATE chat_sessions SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE chat_sessions SET updated_at = substr(updated_at,1,19) || '.' || substr(CASE WHEN substr(updated_at,20,1)='.' THEN substr(updated_at,21,length(updated_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(updated_at) BETWEEN 20 AND 29 AND updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(updated_at)=20 OR (substr(updated_at,20,1)='.' AND substr(updated_at,21,length(updated_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE chat_session_turns SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
