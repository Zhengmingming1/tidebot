# Per-task cost controls

Each task has one operational cost policy, separate from its immutable prompt versions:

- **Task limits (`task`)**: enforce task run/day limits and shared global/category budgets, including monetary and token limits.
- **Global limits only (`global`)**: retain saved task limits but ignore them; enforce shared global/category monetary and token budgets.
- **Bypass budgets (`bypass`)**: ignore all three Tidebot budget scopes, including token caps. The editor requires an explicit confirmation before selecting this mode.

All modes continue to record usage and obey provider quotas, explicit caller caps, dry-run safety caps, timeouts, user stops and security restrictions. Bypass is not free usage. Its spending still counts in shared totals, so it can exhaust the budget available to other tasks.

## Editing and currency

The editor uses the global USD/CNY display setting and conversion rate. Disabling task limits preserves their amounts for later re-enabling. Policy revisions and an FX fingerprint reject stale edits with HTTP 409 rather than silently interpreting amounts in another currency. Budget-paused tasks are not automatically resumed by changing their policy.

**Unlimited first.** New tasks start with no per-run or daily cap of their own; only the optional global daily budget applies (none set = unlimited, no reminder). Runs the owner starts outside a task (Run dialog, rerun, buttons, chat session turns, workflow nodes) carry no per-run cap and no time limit either. Limits are added later, per task or workflow. Helper analyses tidebot starts itself (dependency check, skill analysis, comparisons, dry runs) keep their explicit caps.

Migration 034 materialized the old effective USD 0.05 limit for existing tasks. Migration 044 removes it again where the owner never touched it: mode `task`, no token cap, amount exactly USD 0.05 (or CNY 0.05 × the current rate), no policy edit that ever changed the per-run amount, and not a cap that 034 lowered from the owner's own value. Each removed row is written to `operation_log` (`unlimited_default_migration`); the spend page shows a one-time notice listing the tasks with **Restore all**, which writes the caps back (converted if the display currency changed; a cap set again since is kept). Daily caps, token caps and caps the owner set stay unchanged. Existing tasks keep their mode. Task duplication and version-3 task export/import preserve the policy; imported amounts are converted from the exported currency using the destination rate.

## Execution and accounting

A queued run rechecks policy immediately before launch. Launch atomically checks and reserves the remaining selected budgets, then stores an immutable effective-policy snapshot on the run. Policy/currency changes affect later launches, not an already running process. The run freezes its accounting day and FX at launch; historical ledger rows retain their original date interpretation.

Concurrent runs reserve money and tokens to prevent both seeing the same remaining allowance. Runtime enforcement observes streamed usage and stops after a limit is exceeded; in-flight provider work and late usage reports can still produce a small overshoot. These are application controls, not a provider-side billing guarantee. If no smaller per-run token limit exists, the remaining shared token allowance is conservatively reserved by the first run. This can serialize budget-controlled runs; bypass runs do not reserve shared budgets. Provider subscription quota reservations are independent of the task's budget mode.

Streaming usage replaces the run's cumulative ledger entry and reduces its outstanding reservation atomically. If final ledger persistence fails, the process retains the reservation and refuses new runs rather than silently losing spend. Investigate the database error before restarting; a restart alone does not repair missing ledger data.

## Rollout and rollback

Back up the Tidebot SQLite database before upgrading. Migration 034 is additive, but it also materializes legacy run caps. To fully roll back both schema and cap values, restore the pre-upgrade database and previous binary together. Do not try to reverse the migration with ad-hoc SQL while runs are active.
