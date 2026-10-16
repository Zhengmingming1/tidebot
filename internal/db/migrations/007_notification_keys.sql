-- Structured notification text: message key + JSON params, rendered by each
-- client in its own language. Old rows keep msg_key='' and fall back to body.
ALTER TABLE notifications ADD COLUMN msg_key TEXT NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN msg_params TEXT NOT NULL DEFAULT '{}';
