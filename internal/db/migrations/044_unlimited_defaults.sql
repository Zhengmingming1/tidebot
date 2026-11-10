-- Unlimited first: remove the implicit per-run cap (USD 0.05) that migration 034
-- and the old task form wrote for the owner. Only caps the owner never touched:
--  * the task uses its own limits (mode 'task'); no token cap on the row;
--  * the amount is the default: USD 0.05, or CNY 0.05 x the current rate (±0.0001);
--  * no cost policy edit ever changed the per-run amount (a form save that kept
--    the prefilled value doesn't count as a change);
--  * 034 didn't lower a cap the owner had set before it.
-- Each removed row is kept in operation_log (append-only), so the spend page can
-- put them all back with one click. Daily caps, token caps and caps the owner
-- set stay exactly as they are.
CREATE TEMP TABLE m044_rate AS
SELECT COALESCE((SELECT CASE WHEN json_valid(value_json) AND json_extract(value_json,'$.usd_cny') BETWEEN 0.1 AND 100
  THEN json_extract(value_json,'$.usd_cny') END FROM settings WHERE user_id='owner' AND key='pricing.fx'), 7.1) AS rate;

CREATE TEMP TABLE m044 AS
SELECT b.id AS budget_id, b.scope_id AS task_id, b.max_usd, b.currency, b.action, b.warn_at_pct
FROM budgets b JOIN tasks t ON t.id = b.scope_id
WHERE b.scope = 'task' AND b.period = 'run' AND b.bucket = ''
  AND t.cost_control_mode = 'task'
  AND b.max_tokens IS NULL
  AND ((b.currency = 'USD' AND abs(b.max_usd - 0.05) <= 0.0001)
    OR (b.currency = 'CNY' AND abs(b.max_usd - 0.05 * (SELECT rate FROM m044_rate)) <= 0.0001))
  AND NOT EXISTS (SELECT 1 FROM operation_log o WHERE o.entity_type = 'task' AND o.entity_id = t.id
    AND o.action = 'cost_policy_migration' AND json_valid(o.detail_json)
    AND COALESCE(json_extract(o.detail_json, '$.max_amount'), 0) > 0)
  AND NOT EXISTS (SELECT 1 FROM operation_log o WHERE o.entity_type = 'task' AND o.entity_id = t.id
    AND o.action = 'cost_policy_update' AND (NOT json_valid(o.detail_json)
      OR json_extract(o.detail_json, '$.Before.run_amount') IS NOT json_extract(o.detail_json, '$.After.run_amount')));

INSERT INTO operation_log(at, entity_type, entity_id, action, actor, reason, detail_json)
SELECT strftime('%Y-%m-%dT%H:%M:%SZ','now'), 'task', task_id, 'unlimited_default_migration', 'system',
  'remove implicit default per-run cap',
  json_object('budget_id', budget_id, 'max_amount', max_usd, 'currency', currency, 'action', action, 'warn_at_pct', warn_at_pct)
FROM m044;

DELETE FROM budgets WHERE id IN (SELECT budget_id FROM m044);
-- An editor opened before the upgrade must reload instead of saving the old cap back.
UPDATE tasks SET cost_policy_revision = cost_policy_revision + 1 WHERE id IN (SELECT task_id FROM m044);

DROP TABLE m044;
DROP TABLE m044_rate;
