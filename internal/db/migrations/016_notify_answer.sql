-- "Notify on success" carries the start of the final answer; per task, on by default.
ALTER TABLE task_policies ADD COLUMN notify_answer INTEGER NOT NULL DEFAULT 1;
