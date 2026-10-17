-- Per-run cost computed by tidebot from token usage (see internal/pricing).
ALTER TABLE runs ADD COLUMN cost_amount REAL NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cost_currency TEXT NOT NULL DEFAULT 'USD';
ALTER TABLE runs ADD COLUMN cost_source TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN requests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN price_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN cost_detail TEXT NOT NULL DEFAULT '';
-- Existing runs only have Claude Code's Anthropic-table figure: mark it as a legacy estimate.
UPDATE runs SET cost_amount = total_cost_usd, cost_currency = 'USD', cost_source = 'legacy';

ALTER TABLE cost_ledger ADD COLUMN amount REAL NOT NULL DEFAULT 0;
ALTER TABLE cost_ledger ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD';
ALTER TABLE cost_ledger ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE cost_ledger ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE cost_ledger ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE cost_ledger ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cost_ledger ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cost_ledger ADD COLUMN requests INTEGER NOT NULL DEFAULT 0;
UPDATE cost_ledger SET amount = usd, currency = 'USD', source = 'legacy';
UPDATE cost_ledger SET provider = COALESCE((SELECT provider_name FROM runs WHERE runs.id = cost_ledger.run_id), ''),
                       model = COALESCE((SELECT model FROM runs WHERE runs.id = cost_ledger.run_id), '');
CREATE INDEX IF NOT EXISTS idx_cost_ledger_provider ON cost_ledger(provider, model);

ALTER TABLE budgets ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD';
