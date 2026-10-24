-- Why a task is paused (user / breaker / budget / pack / plugin), a short detail and when.
ALTER TABLE tasks ADD COLUMN pause_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pause_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN paused_at TEXT;
-- Tasks paused before this keep an empty kind: the reason is unknown, the UI just says paused.
