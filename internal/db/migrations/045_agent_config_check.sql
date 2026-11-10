-- P1 (command-run safety): the after-run agent-config check flags the run with
-- what changed (JSON list of {path, change}; '' = nothing found / not checked).
ALTER TABLE runs ADD COLUMN agent_config_changes TEXT NOT NULL DEFAULT '';

-- Channels that get the "needs you" group also get the new agent-config notice
-- (the group is stored as one rule per event). confirm_needed came from the
-- PreToolUse hook, which never ran under --bare; its rules stay, harmless.
INSERT INTO notify_rules(id, event, task_id, channel_id, created_at)
SELECT 'nru_acc_' || lower(hex(randomblob(8))), 'agent_config_changed', r.task_id, r.channel_id, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM notify_rules r
WHERE r.event = 'confirm_needed'
  AND NOT EXISTS (SELECT 1 FROM notify_rules x WHERE x.event = 'agent_config_changed' AND x.channel_id = r.channel_id AND x.task_id = r.task_id);
