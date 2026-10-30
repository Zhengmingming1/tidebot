-- Old rows are trimmed every 6 hours (internal/app/rowprune.go); these indexes let the
-- trims and the sign-in rate limit find rows by time without scanning whole tables.
CREATE INDEX IF NOT EXISTS idx_login_attempts_ip_at ON login_attempts(ip, at);
CREATE INDEX IF NOT EXISTS idx_login_attempts_at ON login_attempts(at);
CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at);
CREATE INDEX IF NOT EXISTS idx_budget_events_at ON budget_events(at);
