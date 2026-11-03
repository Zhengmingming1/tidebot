-- Sessions (会话): a conversation that continues a finished run, one `claude -p`
-- per turn resumed from Claude Code's transcript in tidebot's own
-- CLAUDE_CONFIG_DIR. ("sessions" is the WebUI login table, hence chat_.)
CREATE TABLE chat_sessions (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  origin TEXT NOT NULL DEFAULT 'run',            -- chat | run | run_fix
  origin_run_id TEXT NOT NULL DEFAULT '',
  workspace_dir TEXT NOT NULL DEFAULT '',
  permission_mode TEXT NOT NULL DEFAULT 'read',
  provider TEXT NOT NULL DEFAULT '',
  bindings_json TEXT NOT NULL DEFAULT '[]',
  claude_session_id TEXT NOT NULL DEFAULT '',
  turns INTEGER NOT NULL DEFAULT 0,              -- turns sent in the session (the origin run is turn 0)
  cost_total REAL NOT NULL DEFAULT 0,            -- USD
  web_access INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'idle',           -- idle | running | closed
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX chat_sessions_updated ON chat_sessions(updated_at);
CREATE INDEX chat_sessions_origin_run ON chat_sessions(origin_run_id);

-- One row per turn; the turn's prompt, answer, cost and log are its run's.
CREATE TABLE chat_session_turns (
  session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
  turn_index INTEGER NOT NULL,
  run_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (session_id, turn_index)
);
CREATE INDEX chat_session_turns_run ON chat_session_turns(run_id);
