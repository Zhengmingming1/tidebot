-- Defaults affect only sessions created after the owner opts in. They never grant tools.
CREATE TABLE capability_session_defaults (
 capability_id TEXT PRIMARY KEY REFERENCES capabilities(id),
 created_at TEXT NOT NULL
);
CREATE TRIGGER capability_uninstall_clear_default AFTER UPDATE OF status ON capabilities
WHEN NEW.status='uninstalled'
BEGIN DELETE FROM capability_session_defaults WHERE capability_id=NEW.id; END;

-- Session JSON is the public binding source. Keep normalized references in sync
-- for all writers, including proposal installation and session deletion.
INSERT OR IGNORE INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at)
SELECT 'session',s.id,c.id,'',s.updated_at FROM chat_sessions s
JOIN json_each(s.bindings_json,'$.capabilities') j
JOIN capabilities c ON c.id=j.value AND c.status='installed';
CREATE TRIGGER chat_session_capabilities_sync AFTER UPDATE OF bindings_json ON chat_sessions
BEGIN
 DELETE FROM capability_bindings WHERE owner_kind='session' AND owner_id=NEW.id
 AND capability_id NOT IN (SELECT value FROM json_each(NEW.bindings_json,'$.capabilities'));
 INSERT OR IGNORE INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at)
 SELECT 'session',NEW.id,c.id,'',NEW.updated_at FROM json_each(NEW.bindings_json,'$.capabilities') j
 JOIN capabilities c ON c.id=j.value AND c.status='installed';
 DELETE FROM capability_tool_grants WHERE session_id=NEW.id
 AND capability_id NOT IN (SELECT value FROM json_each(NEW.bindings_json,'$.capabilities'));
END;
CREATE TRIGGER chat_session_capabilities_delete AFTER DELETE ON chat_sessions
BEGIN DELETE FROM capability_bindings WHERE owner_kind='session' AND owner_id=OLD.id; END;
CREATE TRIGGER chat_session_capabilities_insert AFTER INSERT ON chat_sessions
BEGIN
 INSERT OR IGNORE INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at)
 SELECT 'session',NEW.id,c.id,'',NEW.updated_at FROM json_each(NEW.bindings_json,'$.capabilities') j
 JOIN capabilities c ON c.id=j.value AND c.status='installed';
END;
