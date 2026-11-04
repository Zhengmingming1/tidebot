-- Operational settings deliberately live outside immutable task content versions.
ALTER TABLE tasks ADD COLUMN cost_control_mode TEXT NOT NULL DEFAULT 'task'
  CHECK(cost_control_mode IN ('task', 'global', 'bypass'));
ALTER TABLE tasks ADD COLUMN cost_policy_revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE runs ADD COLUMN effective_cost_policy_json TEXT NOT NULL DEFAULT '';

-- Preserve original budget rows before materializing the old effective $0.05 cap.
INSERT INTO operation_log(at, entity_type, entity_id, action, actor, reason, detail_json)
SELECT strftime('%Y-%m-%dT%H:%M:%SZ','now'), 'task', scope_id,
 'cost_policy_migration', 'system', 'materialize legacy effective run limit',
 json_object('budget_id',id,'max_amount',max_usd,'currency',currency,'max_tokens',max_tokens,
 'action',action,'warn_at_pct',warn_at_pct,'bucket',bucket)
FROM budgets WHERE scope='task' AND period='run';

-- A missing setting means the existing software default. Invalid settings also used that default.
CREATE TEMP TABLE migration_cost_fx AS
SELECT CASE WHEN json_valid(value_json) THEN value_json ELSE '{}' END AS value
FROM settings WHERE user_id='owner' AND key='pricing.fx';
INSERT INTO migration_cost_fx(value) SELECT '{}' WHERE NOT EXISTS(SELECT 1 FROM migration_cost_fx);
UPDATE migration_cost_fx SET value='{}' WHERE
 COALESCE(json_extract(value,'$.display'),'USD') NOT IN ('USD','CNY') OR
 COALESCE(json_extract(value,'$.usd_cny'),7.1) NOT BETWEEN 0.1 AND 100;

UPDATE budgets SET max_usd = min(
 CASE WHEN max_usd > 0 THEN max_usd ELSE 1.0e300 END,
 CASE WHEN currency='CNY' THEN 0.05 * COALESCE(
  (SELECT CASE WHEN json_extract(value,'$.usd_cny') BETWEEN 0.1 AND 100
   THEN json_extract(value,'$.usd_cny') END FROM migration_cost_fx LIMIT 1),7.1)
 ELSE 0.05 END)
WHERE scope='task' AND period='run';

INSERT INTO budgets(id,scope,scope_id,period,max_usd,action,warn_at_pct,bucket,created_at,updated_at,currency)
SELECT 'cost-policy-' || t.id,'task',t.id,'run',
 CASE WHEN json_extract(f.value,'$.display')='CNY' THEN 0.05 *
  CASE WHEN json_extract(f.value,'$.usd_cny') BETWEEN 0.1 AND 100 THEN json_extract(f.value,'$.usd_cny') ELSE 7.1 END
 ELSE 0.05 END,
 'pause_notify',80,'',strftime('%Y-%m-%dT%H:%M:%SZ','now'),strftime('%Y-%m-%dT%H:%M:%SZ','now'),
 CASE WHEN json_extract(f.value,'$.display')='CNY' THEN 'CNY' ELSE 'USD' END
FROM tasks t CROSS JOIN migration_cost_fx f
WHERE NOT EXISTS(SELECT 1 FROM budgets b WHERE b.scope='task' AND b.scope_id=t.id AND b.period='run' AND b.bucket='');
DROP TABLE migration_cost_fx;

-- Freeze the accounting day for new ledger entries; NULL preserves legacy time semantics.
ALTER TABLE cost_ledger ADD COLUMN accounting_day TEXT;
CREATE INDEX idx_cost_ledger_accounting_day ON cost_ledger(accounting_day);
