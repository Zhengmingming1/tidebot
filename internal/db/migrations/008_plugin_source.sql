-- Where a plugin came from: local (upload/dir) | official (built-in catalog) | ai-generated.
ALTER TABLE plugins ADD COLUMN source TEXT NOT NULL DEFAULT 'local';
-- AI plugin builder jobs (spec, status, generated version) for the plugin detail chat.
CREATE TABLE IF NOT EXISTS plugin_ai_messages (
  id TEXT PRIMARY KEY,
  plugin_id TEXT NOT NULL,
  role TEXT NOT NULL,          -- user | assistant
  body TEXT NOT NULL,
  version_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plugin_ai_messages ON plugin_ai_messages(plugin_id, created_at);
