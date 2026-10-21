# tidebot backlog

One small round a day: what shipped, what we learned (and from which project),
and ideas for later. Releases are cut weekly; these entries feed the notes.

## 2026-10-20

**Done**

- **Quitting no longer abandons running runs.** Desktop Quit, Ctrl+C and SIGTERM
  (systemd, Docker, logout) used to end the process with runs still going. Each run
  stayed "running" until the next start closed it as interrupted, with $0 spend, and
  Claude Code's process group could outlive the core. Now quitting stops the
  scheduler, refuses new runs (retries, webhooks, buttons) and releases runs still
  waiting for a slot. Running runs get `shutdown_grace` (default 5s,
  `TIDEBOT_SHUTDOWN_GRACE`) to finish on their own. Anything still going after that is
  stopped: its process tree is killed, the row is closed as `canceled` with the
  tokens used so far, and the run log ends with "tidebot is quitting — run stopped".
  The final write gets at most 3s, so quitting always takes under ~8s, which stays
  inside Docker's 10s stop timeout. Run detail shows the reason in your language:
  「已停止：tidebot 退出时这次运行还没结束…」 (`Runner.Drain`, `App.drainRuns`).
- A run stopped this way is not a failure. There's no "run failed" notice, no retry,
  and it doesn't count toward the circuit breaker (before, three quits in a row could
  pause a task).
- **agentd:** a second Ctrl+C / SIGTERM quits right away, and the log says so.
  A blocking `agentctl tasks run` is no longer canceled the moment the signal arrives.
  IPC stays up while runs drain, so a second core can't start and "recover" runs that
  are still writing.
- **Desktop on macOS:** Wails ends the process inside `[NSApp terminate:]`, so
  nothing after `app.Run()` ever ran on Mac: no core shutdown and no relaunch after a
  self-update. Core shutdown (and the relaunch) now runs from `PostShutdown`, which
  fires on all three platforms after the window, tray and single-instance lock are
  gone. It's guarded so it only runs once.
- WebUI shutdown is bounded at 2s. An open live-log stream (SSE) never goes idle, and
  before this `http.Server.Shutdown` waited on it with no deadline.

**Learned**

- n8n `packages/cli/src/active-executions.ts`: `shutdown()` first disables
  concurrency control "so that throttled executions cannot resume during shutdown",
  then waits for active executions. `cancelRunningExecutions()` /
  `recordAsCancelled()` record the cancel under a write deadline "so a stalling
  database cannot hold up shutdown". `Runner.Drain` follows the same shape: release
  the queue → grace → cancel → bounded write.
- n8n `packages/cli/src/commands/base-command.ts` `onTerminationSignal()`: a
  force-shutdown timer (`gracefulShutdownTimeoutInS`) and "Already shutting down" on a
  repeat signal. `start.ts` `stopProcess()` stops triggers before
  `ActiveExecutions.shutdown()`. We use Go's `signal.NotifyContext` stop func, which
  restores the default handler so a second signal kills the process.
- Uptime Kuma `server/server.js` `shutdownFunction()`: stop the server, stop each
  monitor, then close the database. It runs under `http-graceful-shutdown` with a 30s
  timeout and `forceExit`. Same order as ours: scheduler → runs → WebUI → IPC → DB.
- Wails v3 beta.30 `pkg/application/application_darwin_delegate.m`
  (`applicationShouldTerminate` returns `NSTerminateNow` after `cleanup()`) and
  `application_options.go` (`PostShutdown`: "on platforms where the Run() method does
  not return"). That's why the Mac path never reached the code after `Run()`.
- Reproduce: `scripts/demo-webui.sh`, restart agentd with
  `TIDEBOT_MOCK_STEPS=60 TIDEBOT_SHUTDOWN_GRACE=2s`, start a task, open its live log,
  `kill -TERM`. It exits in ~2.6s with the row `canceled` and no orphaned children.

**Ideas for later**

1. Tray Quit while runs are going: show "退出并停止 2 个运行？" via Wails `ShouldQuit`,
   or put the count in the menu item label.
2. Save partial token usage during a run, every N requests (still open). Quit now
   records usage, but a crash or power loss still records $0.
3. Optional "rerun runs stopped by quit/crash on next start" per task, off by default.
4. Docker/compose docs: `stop_grace_period` when raising `shutdown_grace` above 5s.
5. Favicon badge; refresh README screenshots at the Friday release (carried over).

### 2026-10-20 (third pass): export spend as CSV

**Done**

- **Spend → 花在哪了 has a download button** (next to 7 天 / 30 天 / 全部). It saves
  `tidebot-spend_<first day>_to_<today>.csv` with one row per run in the selected range:
  date, time (server zone), task (快速运行 for ad-hoc runs), run id, bucket, provider,
  model, status, requests, input / output / cache read / cache write tokens, amount
  in the display currency, currency, pricing source (按单价计算 / 估算 / 订阅套餐 …).
  The header and labels follow the UI language. A UTF-8 BOM is added so Excel shows Chinese
  task names correctly. Details are in the button's tooltip.
- **Safety:** same session auth as the rest of `/api/` (GET only). Free-text cells
  (task names, provider/model ids) that start with `= + - @`, tab or CR get a leading `'`
  so a spreadsheet won't run them as formulas (OWASP CSV injection). Every value comes from
  parameterized SQL. The response is `no-store`. The file is capped at 100k rows.
- **Resource use:** the CSV is built in memory and written after the query finishes. The
  store has a single connection, so streaming to a slow client would block the scheduler.
  The all-time first-day lookup also runs before the cursor opens. The first version
  deadlocked on exactly this, and the test caught it.
- Tooltips on the last control of a card header now open leftwards instead of being cut off
  at the window edge.

**Learned**

- LiteLLM `ui/litellm-dashboard/src/app/(dashboard)/usage/_components/components/EntityUsage/teamUserSpend.ts`:
  `Papa.unparse(…, { escapeFormulae: true })`, and the file name carries the range
  (`team_user_spend_<start>_to_<end>.csv`). `TeamUserSpendCard.tsx` puts a small
  "Download CSV" button on the usage card. `src/components/TeamsPage/teamsCsvExport.ts`
  does the same for the teams table. We borrowed formula escaping, the range in the filename
  and the button on the card itself.
- `fetch().text()` drops a UTF-8 BOM. Download through `res.blob()` to keep the
  bytes as the server sent them.

**Ideas for later**

1. Export the current breakdown (grouped rows) as well as per-run rows.
2. Report the silent warning when a task duplicate couldn't copy budgets/policy (from the second pass).

### 2026-10-20 (second pass): duplicate a task

**Done**

- **Duplicate task.** There's a copy icon next to delete on task detail; hovering it explains
  what's copied. It makes "Daily Report 副本" (or "Daily Report (copy)" in English) and opens it.
  Undo in the toast archives the copy again. The copy keeps the full current spec
  (prompt, goal, amended constraints, skill binding, allowed tools, budget cap,
  timeout), the schedule, task budgets and the run policy. It **starts paused**, so a
  scheduled task never fires twice by accident. It gets a fresh v1. Runs, version
  history, triggers/webhooks (their tokens must stay unique) and per-task notification
  rules are not copied. The name is made unique on the server ("副本 2", "副本 3", …,
  cut to 80 characters with the number kept), and archived tasks don't block a name.
- API: `POST /api/tasks/{id}/duplicate {name?}` (session + CSRF like the rest of
  `/api/tasks/`), `tasksvc.Duplicate`, `tasksvc.UniqueName`.

**Learned**

- n8n `packages/frontend/editor-ui/src/features/workflows/components/DuplicateWorkflowDialog.vue`
  strips `active`/`activeVersion`/ids before `saveAsNewWorkflow`, so a copy is never
  live. The name comes from `getDuplicateCurrentWorkflowName` in
  `packages/frontend/editor-ui/src/app/stores/workflows.store.ts`: it asks the server
  for a free name. We do the same: paused copy, server-side unique name.
- Uptime Kuma `src/pages/EditMonitor.vue` (`isClone`) clears the push token and
  other per-instance fields and names the clone `$t("cloneOf", [name])`. That's
  why we don't copy webhook triggers.

**Ideas for later**

1. "Duplicate" in the task list row menu and the command palette (`d` on task detail).
2. Optionally copy triggers too, with new webhook tokens.

## 2026-10-19

**Done**

- **Runs left over by a crash are closed on the next start.** If the core died mid-run
  (crash, force quit, laptop out of battery, OS restart), the run stayed "running"
  forever: Home showed it as live, the tray counted it, and with the default overlap
  policy `skip` the task never fired again. Now the core closes those rows as
  `canceled` with a stable `interrupted:` message before the scheduler starts, and sends
  one "运行中断 / Run interrupted" notice per run (at most 5). These go through the same
  rules as failures, so a "run failed" email rule covers them too. Run detail shows the
  reason in the viewer's language (`internal/app/recover.go`,
  `Store.RecoverInterruptedRuns`).
- **One core per IPC path on macOS/Linux.** `ipc.Listen` used to delete the socket
  file without checking it, so starting agentd while the desktop app was running
  quietly took over the socket and two schedulers shared one database. Now it dials
  first: a socket that answers is refused with `ipc.ErrInUse`, and a stale one is
  replaced. Windows already got this from the first-instance named pipe. Hitting
  `ErrInUse` no longer counts as a failed start for the self-update rollback.
- **Relative times stay true.** "5 分钟前" used to freeze when you left a page open.
  Now there's one shared 30s ticker (paused while the window is hidden), and hovering a
  relative time shows the full local date and time zone. This covers task detail,
  triggers, the amendment thread, optimizer proposals and the version tree.
- **Tab title shows the page and live state:** `▶ 2 个运行中 · 任务 – tidebot`, `⚠` when
  Claude Code or the provider needs a look, and the item name on detail pages.
- Wording: task status "active" is now **已启用** instead of 运行中, which looked the
  same as the run status "running" in the same table.

**Learned**

- n8n handles the same failure at startup: `packages/cli/src/eventbus/message-event-bus/message-event-bus.ts`
  (`performStartupRecovery`) collects unfinished executions and calls
  `ExecutionCrashService.markAsCrashed(ids, 'startup-recovery')`
  (`packages/cli/src/executions/execution-crash.service.ts`). The repository
  (`packages/@n8n/db/src/repositories/execution.repository.ts`) guards the transition
  on status and announces each crashed execution. tidebot does the same with a status
  guard in the UPDATE and a per-run notice. It reuses `canceled` rather than adding a
  new status, so stats, filters and the rerun button work unchanged.
- n8n `packages/frontend/@n8n/composables/src/useDocumentTitle.ts`: status prefix
  (🔄 / ⚠️ / ▶️) + name + product. That's the shape we used for the tab title.
- Open WebUI `src/lib/components/workspace/Skills.svelte`: `dayjs().fromNow()` inside a
  `Tooltip` whose content is the full `LLLL` date. Open WebUI doesn't refresh the text
  afterwards; we added the shared ticker.
- Restarting the demo with `scripts/demo-webui.sh` + kill reproduces the orphaned run.
  A rerun of the audit is a quick way to catch this class of bug.

**Ideas for later**

1. Save partial token usage during a run (every N requests) so interrupted runs still
   count toward spend and budgets. Right now they record $0.
2. Optional "rerun interrupted scheduled runs on start" per task (Cronicle/n8n-style
   recovery), off by default because runs aren't guaranteed idempotent.
3. Graceful quit: on desktop Quit / SIGTERM, wait a few seconds for running runs to
   write their final state before closing the database.
4. Favicon badge (dot) for failures/needs-confirmation, alongside the title prefix.
5. Refresh README screenshots (the task status pill now reads 已启用) at the Friday
   release.

## 2026-10-20 (UI pass): five visible fixes, two of them auto-fill

I audited every page at 1024×655 and 1440×900 in zh-CN. I picked the five weak spots
that showed up most, plus two smaller fixes I made along the way. Before/after shots
are kept outside the repo.

| # | Kind | What changed | Borrowed idea from |
|---|------|--------------|--------------------|
| 1 | visual | **Buttons page cards**: left-aligned on a fixed rhythm. Each card has a tinted icon tile, a title with a 2-line description, and a footer naming the target. Edit, delete and run show on hover (always on touch). | Open WebUI `src/lib/components/workspace/Models.svelte` (`group-hover` actions, `line-clamp`/`truncate`) |
| 2 | visual | **Spend chart**: every day of the range is on the axis, zero days show as grey stubs and today is highlighted. The scale is rounded to $top/$mid/$0 with gridlines. Each bar has a tooltip with date + amount, and an all-zero range shows an empty state. | Uptime Kuma `src/components/HeartbeatBar.vue` (fixed slots with empty placeholders, per-beat tooltip) |
| 3 | visual | **Log viewer**: a one-row icon toolbar with tooltips. Rows read like a transcript: repeated timestamps are muted, and `assistant:` is dropped. Tool calls show as a chip with a one-line input preview (command / file / pattern) and expand on click. | opcode `src/components/ToolWidgets.tsx` (`BashWidget`, `ReadWidget`) and `src/components/StreamMessage.tsx` |
| 4 | auto-fill | **New task**: the instruction comes first. Writing "每个工作日早上 9 点半汇总…" or "every Monday at 9:30am…" fills the name and the schedule (`POST /api/tasks/infer`, zh+en rules). A schedule is only filled when a frequency word is there. Fields the user has edited are never overwritten, and an 自动 pill plus a note say what was filled. The default provider falls back to one with a key, and choosing a skill names the task. | Open WebUI `backend/open_webui/utils/task.py` (`title_generation_template`: name a chat from its first message). Here it's done with deterministic rules, no model call. |
| 5 | auto-fill | **Add provider**: paste a Base URL, an `export ANTHROPIC_…` block, or a Claude Code `settings.json` env object. That picks the preset and completes a bare host to the official endpoint (also stripping `/v1/messages`). It fills a free name (`deepseek-2` if taken), the auth header, models and extra env. A URL pasted into the key box moves to Base URL. Only the first 7 key characters go to the server, which is enough to spot `sk-ant-`. | Cherry Studio `src/shared/utils/api/format.ts` (`formatApiHost`: normalize what users paste as the API host) |
| + | visual | Runs table: one line per row, with a provider/model column instead of repeating the task name. | — |
| + | usability | Dialogs: sticky header with a close button (Esc in its tooltip), sticky action bar, and edge shadows while scrolled, so long forms fit 1024×655. | shadcn/ui `apps/v4/registry/new-york-v4/ui/dialog.tsx` (`showCloseButton`, `DialogHeader`/`DialogFooter`) |

Ideas only, no code copied. Every new string is escaped, nothing is inline (CSP), and
the infer endpoints are POST + CSRF so prompts stay out of URLs and logs.

**Ideas for later**

1. Task auto-fill could also suggest a budget from the provider's recent per-run cost
   (the spend data is already there) and a timeout from the skill's past durations.
2. Plugin config forms: prefill from the manifest's examples and remember the last
   values per plugin.
3. Button form: suggest an icon from the label and target ("备份" → database, "报告" → doc).
4. Provider auto-fill: offer "check models" right after a paste once the key is saved.

## 2026-10-21 (10:00): storage settings — see what the data folder takes and clean it from the WebUI

Category: **resource usage** (disk). The last two rounds were reliability (graceful quit)
and UI/auto-fill, so this one goes to resources.

**Done**

- **Settings › Data › Storage card.** A stacked bar plus legend shows how much space run
  logs, artifacts, the database (incl. WAL) and backups take. File counts are in the
  hover tooltips, and the total sits in the card header. Hovering the ⓘ next to the
  total shows what lives there and the last cleanup (automatic or manual, time + amount
  freed), so none of that meta info sits inline.
- **Retention is now a setting instead of a hard-coded constant.** You can set how long
  to keep run logs and artifacts (7 days up to 365, or forever), how many of each task's
  latest runs to always keep, and a size cap (1–50 GB or no cap). All four are dropdowns
  with no typing, and they autosave with 已保存 ✓. The 6-hourly background job and
  `retention.run` use the stored policy. A corrupt or out-of-range stored value falls back
  to the defaults (30 d / 90 d / 20 / 10 GB) rather than deleting more than expected.
- **立即清理 / Clean up now with a preview.** The row says what the current rules would
  delete ("可删除 31 个文件，释放 28.3 MB"), and the preview follows any dropdown change
  immediately. The button is disabled when there's nothing to delete. The confirm dialog
  repeats the numbers and what's kept (important runs, the latest N per task, run history
  and spend).
- **压缩数据库 / Shrink database.** `VACUUM` + `wal_checkpoint(TRUNCATE)`. The button only
  enables when at least 1 MB of SQLite free pages (`freelist_count × page_size`) can be
  returned to the disk. Otherwise the row says the database is already compact.
- **Fix:** the size cap could delete the log of a run that was **still running**. Queued
  and running runs are now protected like important ones. Also `LogDays/ArtifactDays = 0`
  now means "forever" (before, 0 would have deleted everything on the next tick).
- The command palette has "设置 › 存储空间", which jumps to `#/settings/data/storage`.
- New RPCs (WebUI via `/api/rpc/`, CSRF + session like the rest): `storage.get`,
  `storage.settings` (partial update, range-checked), `storage.cleanup`, `storage.vacuum`.
  Responses carry sizes and counts only, never paths or file names.
- Tests: `internal/app/storage_test.go` covers the preview not touching files, running
  runs being protected, forever=0, partial updates and validation, the corrupt-setting
  fallback, the auto tick using the stored policy and recording only when it freed
  something, and VACUUM returning space.

**Inspiration**

- Uptime Kuma `src/components/settings/MonitorHistory.vue`: "keep data for N days, 0 =
  infinite", with the SQLite size shown right on the "Shrink Database" (VACUUM) button.
  `server/jobs/clear-old-data.js` re-reads the setting on every run, falls back to the
  default when it can't be parsed, and treats < 1 as "don't delete". tidebot uses the
  same semantics. What tidebot adds: per-kind usage, a dry-run preview before deleting,
  and the cleanup record in a tooltip.

**Learned**

- In WAL mode a `VACUUM` alone doesn't shrink the files on disk until a checkpoint runs.
  `PRAGMA wal_checkpoint(TRUNCATE)` right after makes the saving visible immediately.
- Splitting retention into plan + apply made the preview free. The same plan drives
  the button state, the confirm text and the actual delete, so they can't disagree.

**Ideas for later**

1. Per-task retention override (e.g. keep a noisy "every 5 min" task's logs for 3 days only).
2. Show the biggest tasks by log size in the storage tooltip/drawer so users know which task
   to tune.
3. Warn (notification) when free disk space on the data volume drops under ~1 GB, with a
   link to this card.
4. Let backups have their own "keep last N manual backups" setting next to the auto ones.
