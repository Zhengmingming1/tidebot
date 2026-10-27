-- A channel that failed DownAfter sends in a row raises one "channel down" notice (bell,
-- desktop, the other channels) and one "working again" notice when it recovers.
ALTER TABLE notify_channels ADD COLUMN down_notified INTEGER NOT NULL DEFAULT 0;
-- Channels that get the "system" group receive the two new events too.
INSERT INTO notify_rules(id, event, task_id, channel_id, created_at)
SELECT 'nru_' || lower(hex(randomblob(12))), 'channel_down', '', channel_id, MIN(created_at)
FROM notify_rules WHERE event='disk_low' AND task_id='' GROUP BY channel_id;
INSERT INTO notify_rules(id, event, task_id, channel_id, created_at)
SELECT 'nru_' || lower(hex(randomblob(12))), 'channel_up', '', channel_id, MIN(created_at)
FROM notify_rules WHERE event='disk_low' AND task_id='' GROUP BY channel_id;
