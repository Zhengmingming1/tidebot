-- Last delivery per notification channel (Settings → Notifications shows it on each row).
ALTER TABLE notify_channels ADD COLUMN last_at TEXT;
ALTER TABLE notify_channels ADD COLUMN last_ok INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_channels ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_channels ADD COLUMN last_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_channels ADD COLUMN last_ok_at TEXT;
ALTER TABLE notify_channels ADD COLUMN fail_streak INTEGER NOT NULL DEFAULT 0;
