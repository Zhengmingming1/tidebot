-- I5: trigger cards show the last fire and its outcome.
ALTER TABLE trigger_bindings ADD COLUMN last_fired_at TEXT;
ALTER TABLE trigger_bindings ADD COLUMN last_result TEXT NOT NULL DEFAULT '';
ALTER TABLE trigger_bindings ADD COLUMN fire_count INTEGER NOT NULL DEFAULT 0;
