-- Notifications cleared from the bell: hidden from the notification center, kept in the
-- log. cleared_at holds the batch token of the clear ('' = still shown), so one undo
-- brings back exactly the rows that one click hid.
ALTER TABLE notifications ADD COLUMN cleared_at TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_notifications_shown ON notifications(cleared_at, created_at);
