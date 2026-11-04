CREATE TABLE capability_tool_discovery (
 version_id TEXT NOT NULL REFERENCES capability_versions(id) ON DELETE CASCADE,
 component TEXT NOT NULL,
 tools_json TEXT NOT NULL,
 discovery_hash TEXT NOT NULL,
 tested_at TEXT NOT NULL,
 PRIMARY KEY(version_id,component)
);
CREATE TABLE capability_tool_grants (
 session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
 capability_id TEXT NOT NULL REFERENCES capabilities(id),
 version_id TEXT NOT NULL REFERENCES capability_versions(id) ON DELETE CASCADE,
 component TEXT NOT NULL,
 discovery_hash TEXT NOT NULL,
 tools_json TEXT NOT NULL,
 granted_at TEXT NOT NULL,
 PRIMARY KEY(session_id,capability_id,version_id,component)
);
