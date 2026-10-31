-- "Undo changes" on an edit run's page takes its patch back out of the folder:
-- changes_undone_at is when (UTC RFC 3339), '' while the changes are in place
-- (never undone, or applied again).
ALTER TABLE runs ADD COLUMN changes_undone_at TEXT NOT NULL DEFAULT '';
