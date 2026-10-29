-- "Only notify when the answer changed" (per task, off by default), and per run the id of the
-- earlier run whose final answer was the same (empty: changed, first, or not compared).
ALTER TABLE task_policies ADD COLUMN notify_changed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN answer_same_as TEXT NOT NULL DEFAULT '';
