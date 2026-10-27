-- The agent's final answer (the stream's "result" text), capped; the log keeps only 400 chars of it.
ALTER TABLE runs ADD COLUMN result_text TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN result_truncated INTEGER NOT NULL DEFAULT 0;
