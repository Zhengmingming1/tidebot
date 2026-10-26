-- Spend by time range (budgets, today's spend, the tray's "budget used up" check) filters
-- cost_ledger on created_at: without these it scanned the whole ledger on every check.
CREATE INDEX IF NOT EXISTS idx_cost_created ON cost_ledger(created_at);
CREATE INDEX IF NOT EXISTS idx_cost_task_created ON cost_ledger(task_id, created_at);
-- Recent failures by status (the tray asks every few seconds): an index range instead of
-- reading every failed run ever recorded.
CREATE INDEX IF NOT EXISTS idx_runs_status_finished ON runs(status, finished_at);
