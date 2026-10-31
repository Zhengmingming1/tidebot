-- Runs that could change their folder ("Can edit" in the Run dialog) record what
-- they changed in a git work tree: file_changes is the file list with line counts
-- (JSON), file_patch the unified diff (capped at 512 KB). Empty otherwise.
ALTER TABLE runs ADD COLUMN file_changes TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN file_patch TEXT NOT NULL DEFAULT '';
