-- Categories remain in the ledger for reporting, but no longer impose separate caps.
DELETE FROM budgets WHERE scope = 'bucket';
-- Token accounting and task-specific limits remain unchanged.
UPDATE budgets SET max_tokens = NULL WHERE scope = 'global';
