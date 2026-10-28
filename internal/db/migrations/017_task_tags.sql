-- Tags group tasks in the list ("日报", "监控", "客户 A"); a task has a few, any spelling,
-- compared without case. Archived tasks keep theirs (they are hidden with the task).
CREATE TABLE IF NOT EXISTS task_tags (
  task_id TEXT NOT NULL REFERENCES tasks(id),
  tag TEXT NOT NULL,
  pos INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (task_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_task_tags_tag ON task_tags(tag);
