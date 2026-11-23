-- Workflow timestamps used RFC3339Nano, which drops trailing zeros, so text
-- ordering was wrong inside one second ("…:05Z" sorted after "…:05.3Z").
-- New rows use a fixed 9-digit fraction; pad old rows the same way. Only
-- values shaped like YYYY-MM-DDTHH:MM:SS[.f{1,8}]Z are touched; empty and
-- other values stay as they are.
UPDATE workflows SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflows SET updated_at = substr(updated_at,1,19) || '.' || substr(CASE WHEN substr(updated_at,20,1)='.' THEN substr(updated_at,21,length(updated_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(updated_at) BETWEEN 20 AND 29 AND updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(updated_at)=20 OR (substr(updated_at,20,1)='.' AND substr(updated_at,21,length(updated_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflow_versions SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflow_runs SET created_at = substr(created_at,1,19) || '.' || substr(CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(created_at) BETWEEN 20 AND 29 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(created_at)=20 OR (substr(created_at,20,1)='.' AND substr(created_at,21,length(created_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflow_runs SET started_at = substr(started_at,1,19) || '.' || substr(CASE WHEN substr(started_at,20,1)='.' THEN substr(started_at,21,length(started_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(started_at) BETWEEN 20 AND 29 AND started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(started_at)=20 OR (substr(started_at,20,1)='.' AND substr(started_at,21,length(started_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflow_runs SET finished_at = substr(finished_at,1,19) || '.' || substr(CASE WHEN substr(finished_at,20,1)='.' THEN substr(finished_at,21,length(finished_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(finished_at) BETWEEN 20 AND 29 AND finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(finished_at)=20 OR (substr(finished_at,20,1)='.' AND substr(finished_at,21,length(finished_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflow_run_nodes SET started_at = substr(started_at,1,19) || '.' || substr(CASE WHEN substr(started_at,20,1)='.' THEN substr(started_at,21,length(started_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(started_at) BETWEEN 20 AND 29 AND started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(started_at)=20 OR (substr(started_at,20,1)='.' AND substr(started_at,21,length(started_at)-21) NOT GLOB '*[^0-9]*'));
UPDATE workflow_run_nodes SET finished_at = substr(finished_at,1,19) || '.' || substr(CASE WHEN substr(finished_at,20,1)='.' THEN substr(finished_at,21,length(finished_at)-21) ELSE '' END || '000000000',1,9) || 'Z'
WHERE length(finished_at) BETWEEN 20 AND 29 AND finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z' AND (length(finished_at)=20 OR (substr(finished_at,20,1)='.' AND substr(finished_at,21,length(finished_at)-21) NOT GLOB '*[^0-9]*'));
