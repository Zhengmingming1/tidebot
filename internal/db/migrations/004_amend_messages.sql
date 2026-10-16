-- Natural-language utterances behind requirement items (chat thread in WebUI).
CREATE TABLE IF NOT EXISTS amendment_messages (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  utterance TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_amend_msg_task ON amendment_messages(task_id, created_at);
