-- One file of an edit run can be undone on its own: changes_undone_files is the JSON
-- array of the paths taken back so far ('' = none). Once every file is back out the
-- run counts as undone (changes_undone_at set, this column back to '').
ALTER TABLE runs ADD COLUMN changes_undone_files TEXT NOT NULL DEFAULT '';
