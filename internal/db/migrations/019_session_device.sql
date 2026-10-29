-- Signed-in devices: browser and OS read from the User-Agent at sign-in (names only, never the
-- raw header), shown in Settings › Security so each session can be told apart and ended.
ALTER TABLE sessions ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN os TEXT NOT NULL DEFAULT '';
