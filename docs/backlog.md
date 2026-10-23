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

## 2026-10-21 (16:00): low disk space warning (macOS / Linux / Windows)

Category: **cross-platform + resource usage**. Today's other changes were WebUI forms
(budget suggestion) and storage settings, so this one is about the OS side: knowing when the
disk under the data folder is about to fill up, on all three platforms.

**Done**

- **`internal/diskspace`**: free/total for the volume holding a path. macOS/Linux use
  `statfs` (`Bavail`, so blocks reserved for root don't count as free). Windows uses
  `GetDiskFreeSpaceEx` ("free to caller", which respects quotas). A file path is measured
  through its folder, since Windows only accepts directories. Other platforms return
  `ErrUnsupported` and the check is skipped. Unicode/space paths are tested.
- **Watcher in agentd** (`internal/app/diskwatch.go`), every 15 min: it checks the data
  folder and, if the database lives elsewhere, that folder too, and uses the tighter one.
  The threshold is 5% of the volume, clamped to 1–5 GB (a 20 GB VPS warns at 1 GB, a
  256 GB Mac at 5 GB).
  - Crossing it sends one `disk_low` notification (bell, configured channels, desktop).
  - While still low, it sends at most one reminder a day.
  - It re-arms only after free space is back above threshold + 50%, so a disk hovering
    at the line stays quiet.
  - The last alert time is stored in settings, so a restart doesn't repeat the warning.
- **Tray**: "磁盘空间不足（剩 812 MB）" in the Problems section, and the icon turns red.
  It reads the cached measurement, so the 2-second tray poll never touches the disk.
- **Settings › Data › Storage**: the disk's free/total goes in the tooltip next to the
  total. A sand-coloured warning notice appears above the usage bar **only when low**, with
  the threshold rule in its ⓘ. In the bell, the `disk_low` item links straight to that card
  so cleaning up is one click away.
- zh-CN/en strings for the notification, tray, card and tooltip. The check-i18n
  notification-key list now includes `disk_low`.
- Tests: threshold clamping, warn once → throttle → daily reminder → hysteresis → re-arm,
  no repeat after a restart, the message params, the tray problem appearing and clearing,
  the storage view, and an unsupported platform (omitted, cache cleared). Also a smoke test
  on the real volume.

**Inspiration**

- Coolify `app/Jobs/ServerStorageCheckJob.php` + `app/Notifications/Server/HighDiskUsage.php`:
  a per-server usage threshold, a notification throttle
  (`server_disk_usage_notification_interval_hours`, default 24 h) and
  `RECOVERY_MARGIN = 5` points. The throttle is only released once usage has clearly
  recovered, "so usage that moves around the threshold does not send an alert on every
  change". tidebot borrows the same three ideas (threshold, daily throttle, recovery margin).
  It uses absolute free bytes rather than a percentage, because a laptop at 95% full can
  still have 25 GB left. No code copied.

**Learned**

- `statfs` `Bsize` is `int64` on Linux and `uint32` on macOS. Converting both to `uint64`
  keeps one Unix file for darwin/linux/BSD without separate per-OS files.
- `//go:build unix` (Go 1.19+) covers darwin/linux/BSDs in one tag. `!unix && !windows`
  keeps wasm/plan9 compiling.
- For the screenshot, the `storage.get` response was intercepted in Playwright to fake a
  nearly full disk. That's simpler than adding a test-only knob to the product.

**Ideas for later**

1. When the disk is low, run the retention cleanup once right away (with the normal policy)
   and include "freed X" in the notification.
2. Refuse to start new scheduled runs below a hard floor (e.g. 200 MB) and record them as
   skipped with a clear reason, so they don't fail half-way and corrupt artifacts.
3. A desktop-only "open data folder" button on the storage card (Finder/Explorer/xdg-open).
4. Show the biggest tasks by log size in the storage tooltip (from the 10:00 run's list).

## 2026-10-21: deferred ideas from the 10-20 UI round

All four "ideas for later" from the 10-20 round are done, plus a desktop download check.

**Done**

- **Budget suggestion.** The new-task form fills a *daily* budget with the "自动" tag. Basis,
  in order: similar past runs on that provider (P90 × 1.5), any runs on that provider
  (P75 × 1.5), or the catalog price of a typical run (×3, ×6 for heavy work like refactors).
  Then × runs per day from the schedule. Subscription/free providers get "no cap". The
  tooltip says which basis was used. Typing your own number stops the updates.
- **Button icon and text.** A zh+en keyword map picks the icon from the label, then from the
  target (日报/report → doc, 同步/sync → refresh, 部署/deploy → rocket, 邮件 → mail…). Picking a
  task or skill fills the label and the first sentence of its prompt/description. Each stays
  automatic until you touch it.
- **Plugin prefill** (`internal/prefill`, `POST /api/plugins/prefill`). Email: SMTP/IMAP
  from the address domain, then from MX for company domains (exmail, Feishu, Aliyun,
  M365, Google Workspace…). WeCom/DingTalk: paste a webhook URL or just the key/token; the
  type is detected and the token shown masked. RSS: a site URL is turned into its feed
  (`<link rel=alternate>`, then common paths). Web monitor: a content selector guess.
  Daily digest: timezone, a configured connector, site URLs → feeds. Fetches go through an
  SSRF guard: private/loopback/link-local refused unless you click "仍要检测", proxy-aware,
  redirects re-checked, 1 MB / 15 s caps.
- **Provider paste.** After saving a new provider the drawer offers "检测全部模型". This also
  fixed a crash: the drawer reopened through a stale closure and hit `preset.id` of null.
- Trigger forms remember the last numbers/switches/choices per trigger kind (never URLs,
  text or secrets). Inferred English names no longer end on "…into a".
- **Desktop downloads.** Wails v3 installs no download handler. Under Xvfb, WebKitGTK saved
  `<a download>` blobs silently into the download folder. WKWebView on macOS ignores them
  without a delegate (not verified here). Exports, run logs and recovery codes now go through
  the native Save dialog (`/api/desktop/save`, desktop only). Verified on Linux (WebKitGTK
  2.54, Xvfb): the dialog, the bytes (with BOM), cancel. The new task, button and plugin
  dialogs also worked in the webview.

**Inspiration**

- Thunderbird ISPDB (`thunderbird/autoconfig` `ispdb/qq.com.xml`, `163.com.xml`,
  `googlemail.com.xml`): the domain → host/port/socketType table and the MX fallback idea.
  Values re-checked against provider docs; no XML copied.
- Miniflux `internal/reader/subscription/finder.go`: `<link>` types
  (rss/atom/feed+json) first, then well-known paths (`atom.xml`, `rss.xml`, `feed.xml`…).
- n8n `packages/nodes-base/credentials/Imap.credentials.ts` / `Smtp.credentials.ts`: the
  host/port/secure/user field set the email form maps onto.

**Learned**

- The box resolves every host to 198.18.0.1 (fake-IP proxy), so the SSRF guard must not
  treat 198.18.0.0/15 as private. When going through a proxy, check the *target* host.
- Store budgets are daily, so a per-run estimate needs × runs/day from the cron.
- Under Xvfb the Wails window takes about 10 s to paint; clicks before that are lost.

**Ideas for later**

1. Remember the plugin trigger memory server-side, so it works across browsers and the app.
2. For company domains with no MX match, try Thunderbird's own autoconfig URL
   (`autoconfig.<domain>/mail/config-v1.1.xml`) through the same SSRF guard.
3. "Open folder" after a desktop save (reveal in Finder/Explorer/xdg-open).
4. Verify the macOS/Windows save paths on real machines (CI only builds them).

## 2026-10-22 (10:00): favicon badge for background and pinned tabs

**Done**

- **The tab icon now carries status**, like the title already did (`▶ 2 running`, `⚠`).
  A pinned tab hides the title, so the icon has to carry it on its own. Red count = unread
  failures (run failed / over budget; "9+" above nine). Amber dot = Claude Code or the
  provider needs a look. Green dot = runs going. No badge when all is quiet. "Mark all read"
  and "Reset UI" clear the count.
- Drawn on a 64px canvas from `favicon.svg` and written to both `<link rel=icon>` tags. The
  originals come back when the badge clears. Redraws are debounced 1 s and only happen when
  the badge actually changes.
- **Resource use:** the bell dot used to refresh only on navigation. It now refreshes once a
  minute while the page is visible. In a hidden tab the poll only runs while something was
  running last time we looked, so an idle background tab still makes zero requests. Status
  is fetched first, then notifications, so a run that just failed shows up in that same tick.
- Test: `TestFaviconBadge` (priority failed > alert > running, the "9+" cap, the poll guard).
  It runs `favBadge` under node when node is installed and falls back to static checks
  otherwise.

**Inspiration**

- Uptime Kuma `src/mixins/socket.js`: a `stats.down` watcher calls `favicon.badge(down)`
  through a 1 s debounce with `animation: "none"`. `src/pages/StatusPage.vue` does the same
  with the down-monitor count. We borrowed the count badge, the debounce and "no animation".
  We dropped the favico.js dependency (a plain canvas is enough) and added the running/alert
  states. No code copied.

**Learned**

- Draw from the SVG rather than the 32px PNG. Scaling the PNG up to 64px is visibly blurry
  on Retina tab strips.
- A same-origin SVG drawn into a canvas doesn't taint it, so `toDataURL` works in Chrome,
  Firefox and WebKit.

**Ideas for later**

1. In the desktop app, show the same count on the Dock/taskbar icon (Wails badge API on
   macOS/Windows) instead of the favicon.
2. A setting to turn the badge off, if someone finds the count noisy.
3. Push notification-center changes over the existing SSE connection instead of polling.

## 2026-10-22 (10:13): five extra polish items

**Done**

- **Open folder (desktop app, all three OSes).** The Storage card has a folder button that
  opens tidebot's data folder. After a desktop save, the toast has a "Show in folder"
  action that selects the file. macOS uses `open -R`, Windows `explorer /select,`, and Linux
  the FileManager1 D-Bus call with a fallback to `xdg-open` of the folder
  (`internal/osopen`). The browser never sends a path. `/api/desktop/open` only accepts
  `data` or a random id the save endpoint handed out (the last 20 are kept), and missing
  paths are refused. The button stays hidden in a plain browser. Tests: `TestCommands`,
  `TestOpen`, `TestDesktopOpen`.
- **Meta info moves into tooltips.** Task, skill and run IDs are now a small `ID` chip
  (hover shows the ID, click copies it) instead of a row of their own. The schedule row
  reads "Every day 9:00 · next tomorrow 9:00". The raw cron, the time zone and the next
  runs are in the info tooltip. The skills list shows the description again. Test:
  `TestIDChip` (escaping under node).
- **The log filter bar stays on one row at 1024px.** Copy, download and full screen moved
  into the log card header next to the live/finished state. Their tooltips open downward,
  so the top of the viewport doesn't clip them. Test: `TestLogToolsInHeader`.
- **Quick run remembers.** Up to four recent quick-run prompts appear as chips and filter as
  you type. Click one to fill the prompt. The last provider is preselected (only its name
  is stored, in `localStorage`). Ctrl/⌘+Enter runs, and the hint is in the Run button's
  tooltip. New `GET /api/runs/recent-prompts` uses `Store.RecentQuickPrompts`: distinct
  prompts of runs with no task, parameterized SQL. Tests: `TestRecentQuickPrompts`,
  `TestRecentPromptsAPI`, `TestQuickRunMemory`.
- **Fewer clicks on a run page.** The details card has "Rerun", which starts the task again
  (or the same prompt/provider for a quick run) and opens the new run. A quick run also
  has "Save as task", which opens New task with the prompt and provider filled in.
  Autofill then proposes the name, schedule and budget. Test: `TestRunDetailActions`.

**Inspiration**

- cc-switch `src-tauri/src/commands/config.rs`: `open_config_folder` (creates the folder,
  then opens it) and `reveal_app_log_file` (reveals the file, or the folder if it's gone).
  cherry-studio `src/main/services/file/internal/system/openGuard.ts`:
  `assertOpenableTarget`, which only opens paths that exist. We added the "ids, not paths"
  rule on top.
- Cronicle `htdocs/js/pages/Schedule.class.js` `summarize_event_timing`: a plain-language
  timing summary up front. Healthchecks `static/js/details.js` `.click-to-copy`: an ID you
  click to copy, with a "Click to copy" tooltip.
- opcode `src/components/SessionOutputViewer.tsx`: the output card header holds copy and
  fullscreen next to the title and status.
- Open WebUI `src/lib/components/chat/Suggestions.svelte` (suggested prompts filtered as you
  type) and `ModelSelector.svelte` (remembers the selected model). Also the
  `ctrlEnterToSend` option in `MessageInput.svelte`.
- n8n `packages/frontend/editor-ui/src/features/execution/executions/components/workflow/WorkflowExecutionsPreview.vue`:
  the execution preview header has a Retry dropdown and "Copy to editor". You act on a
  past run right where you're looking at it.

No code copied from any of them.

**Learned**

- A tooltip drawn with `::after` is clipped by `overflow: hidden` on its own element.
  Truncate long chip labels in JS, not with CSS ellipsis.
- Tooltips in a card header near the top of the viewport need to open downward.
- On Linux, `xdg-open` on a file opens the file. Selecting it needs the FileManager1 D-Bus
  call, and the folder is the honest fallback.
- Tests that match app.js text must normalise CRLF (Windows checkouts).

**Ideas for later**

1. "Open workspace folder" on the task page (desktop app), behind the same id-only rule.
2. Pin a recent quick-run prompt so it stays at the front.
3. "Rerun with changes": open the quick-run dialog prefilled instead of starting at once.
4. Verify the FileManager1 reveal on KDE/GNOME and the `explorer /select,` path on a real
   Windows machine (CI only runs the unit tests).

## 2026-10-22 (12:00): usage saved while a run is going

Category: **logic/correctness + resource usage**. The last rounds were UI (favicon badge,
tooltips, quick-run memory), so this one goes back to the core.

**Done**

- **A run's usage is saved every 5 s while it runs**, not only at the end. Before, a crash,
  force quit or power loss left the run at $0 / 0 tokens: the spend page under-counted and
  daily/task budgets let the next run through as if nothing had been spent. Now the runner
  writes the priced cost and tokens so far to the run row (`UpdateRunCost`) and the app
  upserts the `cost_ledger` row (`Options.OnUsage`), so budgets and the spend page see a long
  run's cost *before* it ends. A run closed by startup recovery keeps what it used.
- **Throttle with leading and trailing edge** (`internal/runner/usagesave.go`): the first
  priced request is saved at once, later ones at most once per `Runner.UsageSaveEvery`
  (default 5 s, < 0 = off), the last one of a burst when the interval ends, and nothing when
  the totals didn't change. Saves run on their own goroutine with a 2 s deadline, so a busy
  database never stalls reading Claude Code's output (the store has a single connection).
  `stop` waits for an in-flight save, so a mid-run write can never land after the final one.
- **The final ledger write no longer uses the caller's context.** On quit or user stop that
  context is already canceled, and the write silently failed. It now gets its own 3 s deadline.
- **Memory:** the runner kept every stream-json event (raw JSON, including large tool output)
  in `Result.Events` for the whole run, and nothing ever read it. A long run held megabytes
  per run until it finished. Events are now dropped after parsing.
- **Run detail of a live run** shows "已用花费 / 已用 Tokens" and refreshes those two cards and
  the cost breakdown every 5 s in place (no remount, no flicker; paused in a hidden tab, stops
  when the run ends). The tooltip explains the refresh and that spend counts even if tidebot
  quits. The interrupted-run message now says the usage up to then is kept. zh-CN + en.
- Mock Claude: `TIDEBOT_MOCK_STEP_USAGE=1` makes each demo step a priced request.
- Tests: `TestUsageSaverThrottle`, `TestUsageSaverStopWaitsForSave`, `TestUsageSaverNil`,
  `TestRunUsageSaveOff` (all platforms), `TestRunUsageSavedMidRun` and
  `TestLedgerUpdatedMidRun` (unix, mock script), `TestLiveRunUsage` (escaping under node).

**Inspiration**

- Roo Code `src/core/task/Task.ts`: `debouncedEmitTokenUsage` uses `debounce` with
  `leading: true, trailing: true, maxWait` (2 s), only emits when `hasTokenUsageChanged`
  against the last snapshot, and `emitFinalTokenUsageUpdate()` flushes before abort/finish.
  Its `abortStream` / `updateApiReqMsg` save the partial stream's tokens and cost with the
  cancel reason. We borrowed leading+trailing throttling, change detection and "final write
  wins", and applied it to the database rather than UI events. No code copied.

**Learned**

- A run's usage has two homes (run row for the UI, ledger for budgets); both need the mid-run
  save, or budgets and the spend page disagree after a crash.
- `context.WithoutCancel` (Go 1.21+) plus a short timeout is the right shape for "record this
  even though the run was canceled".
- The ledger row keeps the `created_at`/`day` of its first save, so a run crossing midnight now
  counts toward the day it started on (before: the day it ended).

**Ideas for later**

1. Enforce daily/task budgets *across* concurrent runs mid-run (the ledger is now live, so a
   run could stop once the day's total crosses the cap, not only its own per-run cap).
2. Push the live usage over the existing log SSE stream instead of polling the run every 5 s.
3. `Meter.Now()` re-prices every request on each event; keep running totals for very long runs.
4. Show live cost in the Home "running" row and the tray tooltip.

## 2026-10-22 (14:20): live pages only refetch when something changed

Category: **resource usage**. The last rounds were UI polish and run-usage correctness.

**Done**

- **New `GET /api/rev`** (session auth like the rest of `/api/`): returns
  `"<changes>.<minute>"`. `changes` is SQLite's `total_changes()` on the store's single
  connection, so it moves on every insert/update/delete anywhere in tidebot (runs, mid-run
  usage saves, tasks, settings, notifications) and costs no table scan. `minute` is the
  server's Unix minute, so time-based views (next 24 h, "today") still refresh once a minute
  with no writes.
- **Home** used to re-run `/api/overview` + `/api/dashboard` (schedule preview, heartbeats,
  two cost summaries, ~9 KB JSON) every 5 s while idle and **every second** while a run was
  going, as long as the window was visible. The desktop window often sits open all day. Now
  the elapsed counters still tick locally every second, but the page only asks `/api/rev`
  (23 B, ~0.5 ms) and refetches when the token moved. Change checks run every 2 s while
  something runs, 5 s while you're using the page, and 15 s after 2 minutes without input
  (coming back to the tab counts as input). The **runs list** live poll (2 s while a run is
  queued/running) goes through the same gate. A failed reload is retried on the next check.
- **Session writes:** every authed request (polls included) rewrote `sessions.last_seen_at`,
  which meant a WAL commit and fsync every few seconds just for having the page open. It is
  now written at most once a minute per session (`auth.SeenWriteEvery`). Nothing reads it at
  finer precision. This also keeps `/api/rev` stable while you only read.
- Measured on the demo data (`scripts/demo-webui.sh`), headless Chrome at 1024×655: 31 s on
  an idle home page made 6 `/api/rev` calls and 1 data refresh (the first one). Before it was
  12 heavy calls plus 12 session writes. A button run still shows in "running" within ~0.8 s
  and clears as soon as it ends.
- Also: `gofmt` on `internal/claudebin/resolve.go` (it had drifted).
- Tests: `TestRevAPI` (reads leave the token alone, a write moves it, POST 405, anonymous
  401), `TestRevGate` (node: unchanged token doesn't reload, failed reload retried, minute
  turnover, cadence 2/5/15 s; tolerates CRLF), `TestSessionSeenThrottled`.

**Inspiration**

- healthchecks `hc/front/views.py` `status_single`: the details page polls with `?u=<last
  updated>` and the server only renders and sends the event log when that value changed.
  `static/js/adaptive-setinterval.js`: polls every 3 s for a "quota" of 20 runs after a mouse
  move or the tab becoming visible, otherwise once a minute. We borrowed "cheap version
  check, heavy payload only on change" and "poll slower when nobody is looking", and used
  SQLite's own change counter as the version. No code copied.

**Learned**

- With `SetMaxOpenConns(1)` and no connection lifetime, `total_changes()` is a free,
  process-wide "something was written" counter. It only resets if the connection is
  reopened, and the minute part makes a reset harmless.
- Look for writes on the read path before using a write counter as a version. The session
  touch on every request would have made the token move on every poll.

**Ideas for later**

1. Gate the run-detail live stats (5 s) and the 60 s notification-center poll the same way.
2. Return `rev` in a response header of the heavy endpoints, so the first check after a page
   load can skip the redundant refresh.
3. Long-poll `/api/rev?after=` (wake on the app's event bus), once the per-origin connection
   limit with several open tabs and the run-log SSE has been checked.

## 2026-10-22 (16:00): edit & rerun a quick run

Category: **UX / interaction**. The last rounds were the favicon badge, polish, mid-run usage
saves and change-gated polling, so this one is a user-facing flow.

**Done**

- **Edit & rerun.** A quick (ad-hoc) run's details card has a pencil button next to "重跑"
  (tooltip says what it does, plus the key). It opens the quick-run dialog titled "改后重跑"
  with the run's prompt and provider already filled in, cursor at the end. Run it and the
  edited prompt becomes a new quick run; the original run stays as it was (the hint says so).
  Before, changing one word meant opening quick run and retyping or hunting for the chip.
- **"还原"** shows at the right end of the prompt label only once the prompt or provider
  differs from the original run, and puts both back. CRLF vs LF and trailing whitespace don't
  count as a change (pasting on Windows adds `\r\n`).
- The prompt box grows with its content up to 40% of the window, then scrolls, so a long
  prompt is readable at 1024×655 without dragging the corner. The dialog footer stays visible.
  This applies to the normal quick-run dialog too.
- **Keys:** on a run page `r` reruns and `e` opens edit & rerun (same letters as run/edit on
  a task page). A disabled button (live run) is never clicked by the shortcut. The shortcut
  help line and the button tooltips mention it. zh-CN + en.
- Task runs don't get the pencil: their stored prompt is the composed spec and they run in
  the task workspace with skills, so an ad-hoc copy would quietly behave differently. Editing
  the task is the honest path there.
- Verified in headless Chrome at 1024×655 on the demo data: prefill, provider preselected,
  "还原" hidden → shown → restored, Ctrl+Enter starts a new run with the edited prompt and
  opens it, the original run's prompt is unchanged, no pencil on task runs.
- Test: `TestEditAndRerun` (wiring, the `r`/`e` keys, and `qrEdited` under node: CRLF and
  trailing spaces aren't changes, prompt/provider changes are, leading spaces are).

**Inspiration**

- Open WebUI `src/lib/components/chat/Messages/UserMessage.svelte`: `editMessageHandler`
  copies the sent message into an edit box, autosizes the textarea to `scrollHeight` and
  focuses it; Cmd/Ctrl+Enter confirms, Escape cancels, and the edit is sent as a new branch
  while the old message stays. We borrowed "prefill from what was sent, send as new, keep
  the original" and the autosize. No code copied.

**Learned**

- `:has()` would have been the shortest CSS for the label row, but older WebKitGTK builds on
  Linux desktops don't all support it; a plain class works everywhere.
- Tooltips on controls at the right edge of a dialog have to anchor right, or they get cut off
  by the dialog's own scroll container.

**Ideas for later**

1. Edit & rerun for task runs as a one-off: run the edited prompt in the task workspace with
   the same skills, without creating a task version (needs a backend "override prompt" path).
2. Show a small diff (old → new prompt) in the new run's details when it came from
   edit & rerun, with a link back to the original run.
3. Return the new run id from `/api/runs/quick` and `/rerun` so the UI opens that exact run
   instead of "newest run" after 500 ms.
4. Refresh `run-live.png` in the README at Friday's release (the rerun/save-as-task buttons
   from earlier rounds aren't in it yet).

## 2026-10-22 (18:00): start requests return the new run's id

Category: **logic / correctness (backend)**. The last rounds were all run-page UI, so this
one fixes what sits under them.

**Done**

- **The UI opens the run it started.** Quick run, rerun (run page) and edit & rerun used to
  wait 500 ms and then open "the newest run". With a scheduled run, a webhook or a second tab
  starting at the same moment, that opened somebody else's run; on a slow disk the new row
  wasn't there yet and it opened the previous one. Now `/api/runs/quick`,
  `/api/runs/<id>/rerun`, `/api/tasks/<id>/run` and `/api/buttons/<id>/run` answer with
  `run_id` once the run's row exists, and the UI goes to `#/runs/<run_id>` directly.
- **Start failures are no longer silent.** The start hooks returned before the run had even
  passed its budget check, so "budget blocked", a guard refusal or a broken task spec got a
  cheerful "started" toast and then nothing. The server now waits (up to 3 s) for the run
  row: if the run fails before creating one, that error goes back to the request and shows
  as an error toast.
- **Queued runs are honest.** A run waiting behind the concurrency limit has no row yet; after
  3 s the request answers without an id, the toast says "已排队，有空闲名额时开始运行" and the
  runs list opens. The run keeps going in the background either way.
- The fixed 600–800 ms "refresh a bit later" timers after Run now / rerun on the tasks and
  runs lists and after a home button are gone: the answer comes after the row exists, so
  refreshing right away already shows it.
- `launch()` in `internal/app/app.go` is the one place for this (task runs, quick runs, skill
  button runs); the run's final error is still logged as before. `RunButton` (tray menu)
  keeps its signature. Nothing OS-specific: run ids are plain strings, no paths involved.
- Tests: `TestLaunch` (id on start, early error returned, fast failure still returns the id,
  queued → no id, every failure logged), `TestStartReturnsRunID` (task and quick run ids match
  the stored rows, unix mock claude), `TestStartAnswersWithRunID` (API answers incl. queued and
  budget-blocked) and `TestStartOpensExactRun` (no `api("/api/runs?limit=1")` guess left,
  zh-CN/en key present).

**Inspiration**

- n8n `packages/cli/src/workflow-runner.ts` `WorkflowRunner.run()`: it registers the
  execution (`activeExecutions.add`) and returns the `executionId` straight away, before the
  workflow finishes; a failure before execution start is thrown to the caller
  (`PreExecuteBlockedError`). `packages/frontend/editor-ui/src/app/composables/useRunWorkflow.ts`
  then uses `runWorkflowApiResponse.executionId` to attach to exactly that execution, and its
  stop handler waits (`until(...activeExecutionId)`) when the id isn't known yet. We borrowed
  "answer with the id as soon as the record exists, surface pre-start failures, accept that
  the id can be late". No code copied.

**Learned**

- `runner.Options.OnStart` already fired right after the row insert (it was added for the AI
  plugin builder); reusing it meant no change in the runner at all.
- When a run is created and fails within the wait, both the id and the error arrive. The id
  must win: the run page shows the failure with its log, while an error toast would hide it.

**Ideas for later**

1. Show the queue position in the "queued" toast (and on the runs list) using the runner
   semaphore's waiters.
2. Return 409/402-style codes for budget blocks so the UI can offer "open budget settings"
   in the error toast instead of plain text.
3. Let the tray menu's preset buttons open the new run's page in the desktop window.

## 2026-10-22 (20:30): download a skill as .zip

Category: **missing feature (Skills page)**. The last rounds were all about runs, so this one
is on another page.

**Done**

- **Skills can go back out.** Import took .zip/.skill/.tar.gz/folders, but there was no way
  to get a skill out of tidebot again: to move it to another computer, share it or keep a copy
  you had to dig through the blob store. Now there's a download icon on each row of the skills
  list (current version), a "下载 .zip" button in the skill's header, and a download icon on
  every version in the version tree. Tooltip: "下载 v3（.zip），可在其他电脑直接导入".
- `GET /api/skills/<id>/export[?v=<version>]` builds `<name>-v<n>.zip` with the files under
  one top folder, so Import takes it back as is (tested as a round trip: same name, same file
  count, offered as a new version of the existing skill). Versions of other skills and bogus
  ids get 400, unknown skills 404, signed out 401, POST 405.
- **Same on every OS.** Bytes are stored as imported, no line-ending conversion (a SKILL.md
  written on Windows comes back with its CRLF). Entry names are forward-slash, sorted, with the
  version's creation time, so the same version always gives the same bytes. The folder/file
  name keeps letters and digits of any script and turns everything else (`/\:*?"<>|`, spaces,
  dots) into `-`; Windows device names (`CON`, `COM1`…) get a `-skill` suffix.
- Non-ASCII names: the header sends an ASCII `filename=` plus UTF-8 `filename*=`, and the
  page's `downloadFile` now prefers `filename*` (CSV export unchanged). In the desktop app the
  zip goes through the native Save dialog like the other exports.
- Small one on the way: the skill page showed its raw ID as a mono line under the name; it's
  now the same `ID` chip with a tooltip as on tasks, and the description shows there instead.
- Verified in headless Chrome at 1024×655 (zh-CN, demo data): the list icon downloads
  `changelog-writer-v2.zip`, a version-tree icon downloads `pdf-tools-v1.zip`, tooltips stay
  inside the window.
- Tests: `TestSkillExport` (zip content incl. CRLF, deterministic bytes, Content-Disposition,
  re-import round trip, error codes), `TestExportSlug`, `TestSkillExportUI` (buttons wired;
  `dispName` under node: `filename*` wins, broken escape falls back; tolerates CRLF).

**Inspiration**

- Open WebUI `src/lib/components/workspace/Tools.svelte`: every tool has an "Export" item
  (`exportHandler` saves `tool-<id>-export-<time>.json`), and the page menu has "Export JSON"
  next to "Import JSON" for all tools; the exported file is exactly what Import reads. We
  borrowed "per-item export next to import, in the import format" and made it per version.
  No code copied.

**Learned**

- tidebot's skill name rule (letters, digits, spaces, `. _ -`) already rules out most
  Windows-hostile characters, but not device names or trailing dots; the slug has to handle
  those itself.
- `version_files.mode` is always 420 (0644), so the zip can't carry exec bits; a script that
  needs `+x` loses it in both directions. Worth fixing in the store, not in the export.

**Ideas for later**

1. "Export all skills" as one zip (one folder per skill), next to Import on the skills page.
2. Keep exec bits in `version_files.mode` on import (tar/zip modes) and write them back.
3. Download a task as a bundle (spec + pinned skill versions) to move a whole task.
4. Show the files of a version (`/api/skills/<id>/files`) in a small tree before downloading.

## 2026-10-22 (22:20): static files cached by content hash and sent gzipped

Category: **resource usage / performance (server + browser)**. The last rounds were run and
skill features, so this one is underneath every page.

**Done**

- **Opening the WebUI again no longer downloads everything again.** The embedded files went
  through `http.FileServer` on an `embed.FS`, which has no modification times: no
  `Last-Modified`, no `ETag`, no `Cache-Control`. Browsers couldn't cache or revalidate, so
  every open/reload fetched app.js (378 KB), styles.css (122 KB) and the i18n JSON (68 KB)
  again, uncompressed.
- Now `internal/webui/assets.go` reads the embedded files once, keeps a gzip copy of the text
  ones (js/css/json/svg/html; PNG goes as is) and a strong ETag from the content hash. A build
  version (hash over all files) is put into `index.html` as `?v=<build>` on every `/static/`
  URL; requests with the current `?v=` get `max-age=31536000, immutable`, anything else
  `no-cache` + ETag, so it's a 304. `index.html` itself stays `no-store`, so a new build is
  picked up on the next open, never a stale mix.
- app.js reads `?v=` from its own `<script src>` and adds it to the i18n fetches and the
  favicon-badge image, so those are cached the same way.
- Numbers (demo server, Chrome): first open 378 → 101 KB (app.js), 122 → 23 KB (css),
  68 → 24 KB (zh-CN.json), about 570 → 150 KB in total; opening again in a new tab or a later
  navigation: 0 bytes of static, checked with a counting proxy in front of agentd (only the
  first load reached the server).
- **Same on every OS.** Content types come from a fixed table instead of
  `mime.TypeByExtension`, which reads the Windows registry and can say `text/plain` for `.js`
  there. The desktop window's in-process handler drops `Accept-Encoding`: the webview scheme
  handlers (WKURLSchemeHandler, WebView2, WebKitGTK) don't all decode `Content-Encoding`, and
  there's no network to save, so all three get identity bytes. The loopback bridge and server
  mode are real HTTP and get gzip.
- `HEAD` works, other methods get 405, unknown files and directories 404; `X-Content-Type-
  Options: nosniff` as before. If building the asset table ever failed, it falls back to the
  old file server.
- Tests: `TestAssetsVersionedIndex` (every `/static/` URL in index.html versioned, nothing
  else changed), `TestBuildAssetsVersionFollowsContent` (CRLF vs LF → different build),
  `TestStaticCachingAndGzip` (gzip round trip equals the file, `gzip;q=0` → identity, 304 for
  either ETag variant / weak / list / `*`, immutable only for the current `?v=`, PNG not
  compressed, HEAD/405/404), `TestDesktopStaticIdentity`, `TestAppJSUsesAssetVersion`.

**Inspiration**

- Uptime Kuma `server/server.js`: the built `dist/` is served with `expressStaticGzip("dist",
  { enableBrotli: true })`, and `vite.config.mjs` pre-compresses `js|mjs|json|css|html|svg`
  with gzip and brotli at build time (`vite-plugin-compression`), Vite giving the files
  hashed names. We borrowed "compress the static text once up front, not per request, and
  give the URLs a content version so they can be cached for good", doing it at first request
  from the embedded bytes since we have no frontend build step. No code copied.

**Learned**

- In Chrome over CDP (`Network.enable`), same-tab navigations re-fetched the static files
  even though they were cacheable; without CDP they came from the cache. Measure caching with a
  proxy, not with DevTools attached.
- A `?v=` on index.html's URLs is enough; app.js doesn't need to know any hash at build time,
  it can read its own script URL.

**Ideas for later**

1. Brotli as well (smaller than gzip for app.js), once a pure-Go encoder is worth the
   dependency.
2. Gzip larger JSON API answers (runs list, spend export) when the client accepts it.
3. Split app.js per page (lazy-load the plugin builder and spend charts) to cut the first
   parse time on slow machines.

## 2026-10-23 (00:20): dialogs ask before dropping unsaved edits

Category: **UX / logic correctness (every dialog)**. The last rounds were performance and
run/skill features; this one protects what the user typed.

**Done**

- **A stray Esc or click no longer throws away a long prompt.** Every dialog (new/edit task,
  quick run, provider, button, trigger, plugin settings…) closed on Esc, on a click outside,
  on × / Cancel, and when the browser went Back (the router clears `#modal-root`), with no
  question asked. Now, if the user changed something, the footer turns in place into a warm
  strip "放弃未保存的修改？ [继续编辑] [放弃]" (focus on "继续编辑", a short nudge, none under
  reduced motion). Esc again or "继续编辑" goes back to the field the user was in; "放弃"
  closes. Untouched dialogs close at once as before.
- **What counts as an edit.** A field is tracked only after a trusted `input`/`change` from the
  user, and compared with what it held when the user first touched the dialog (pointer, key or
  focus). So async prefills and our own auto-fill (task name from the prompt, provider from the
  Base URL) never trip it, and typing then deleting makes the dialog clean again. CRLF vs LF is
  not a change. Search boxes and `[data-noguard]` (the "type the name to delete" box) are
  ignored. The `close()` handed to a dialog's code (used after a successful save) never asks.
- **Back / forward:** with edits open, the hash is put back (`replaceState`, no extra history
  entry) and the strip shows; "放弃" then continues to where the user was going.
- **Tab close / reload** in a browser (server mode) gets the browser's own "leave site?"
  prompt via `beforeunload`. Not in the desktop window: WKWebView (macOS) never shows that
  prompt while WebView2 (Windows) does, so it is skipped there to behave the same on all
  three; the desktop window's close goes through the native quit path anyway.
- Fits 1024×655: the strip takes the sticky footer's place, so it is always visible
  (checked in headless Chrome, zh-CN, demo data: quick run and the long new-task form).
- Tests: `TestModalDiscardGuard` (wiring for Esc/backdrop/×/Back/beforeunload, i18n keys in
  both languages; under node: prefill and auto-fill not dirty, CRLF-only not dirty, typed text
  dirty, undone edit clean, search / data-noguard ignored, checkbox and late-added fields
  tracked; tolerates CRLF checkouts).

**Inspiration**

- n8n `packages/frontend/editor-ui/src/app/components/Modal.vue`: `closeDialog()` awaits an
  optional `beforeClose()` and only closes unless it returns exactly `false`; Esc and the
  header × go through the same path. `features/credentials/components/CredentialEdit/
  CredentialEdit.vue` sets `hasUnsavedChanges` only from user data changes, clears it after
  save, and its `beforeClose()` asks "close without saving?" with a "keep editing" choice. We
  borrowed "one guarded close path for every non-save close, dirty = real user edits" and put
  it into our single `modal()` helper so all dialogs get it at once. No code copied.

**Learned**

- Safari/WKWebView doesn't focus checkboxes or buttons on click, so "baseline on focus" alone
  would miss them; taking the baseline on pointerdown/keydown/focusin covers all three engines.
- Only one dialog lives in `#modal-root`; dialogs that open another dialog (e.g. a picker)
  replace the first one, so those flows still drop the outer form. Worth a stacked modal later.

**Ideas for later**

1. Keep a draft of the new-task / quick-run prompt in `sessionStorage` and offer "restore
   draft" when the dialog is opened again after discarding or a crash.
2. Stacked dialogs (picker on top of a form) so a nested choice never replaces the form.
3. Apply the same guard to inline page editors (settings fields, task prompt editing in place).

README screenshots are unaffected (the strip only appears after an attempted close).

## 2026-10-23 (02:20): live log pushed on write instead of polled

Category: **resource usage / CPU (core + browser)**. The last rounds were UX (dialog guard),
static caching and run/skill features; this one is the core's work while a log is open.

**Done**

- **The live log no longer wakes the core twice a second.** While a run detail page was open,
  `/api/runs/<id>/events` reopened the log file *and* asked the database for the run status
  every 500 ms, even when the run printed nothing for minutes (thinking, waiting on a tool).
  The store has a single connection, so those status reads queued with the scheduler and the
  usage saver. New output also showed up 0–500 ms late (≈ 360 ms on average in a test).
- Now the runner rings a per-run "bell" after each write to the log (`runner/logbell.go`,
  `Runner.LogWriter` / `LogWait`), and the SSE handler sleeps on it. Output reaches the page
  in ≈ 17 ms (same test). A burst is read at most every 100 ms and flushed once per batch
  instead of once per line. A 2 s fallback tick still checks the status (a run that ends
  without its final line), the idle cut-off and output written by another process.
- Idle stream, per minute: before 120 file opens + 120 status queries; now 30 `stat`s +
  30 status queries and no opens (the file is only opened when `stat` says it grew, as
  Cronicle does). Nothing changes for runs nobody watches: the bell is one map lookup per
  write and keeps no state without a watcher.
- No leaks: a stream that ends, or a page that is closed mid-run, drops its bell (refcounted;
  checked with `LogWatchers()`); the run's last write wakes anyone left.
- Same on every OS: no file watcher (fsnotify/inotify/FSEvents/ReadDirectoryChangesW behave
  differently and Windows holds the file), only an in-process channel; the reader still
  opens the file briefly per batch so Windows can delete old logs. A trailing `\r` is
  trimmed, so `[[end]]\r\n` ends the stream too.
- Tests: `TestLogBell` (wake, per-run isolation, idempotent done, refcount, release),
  `TestLiveLogPushedOnWrite` (lines arrive with a 1 h fallback, partial line waits for its
  newline, CRLF end marker, no bell left), `TestLiveLogDisconnectReleases`,
  `TestLiveLogFallbackPoll` (no LogWait: file growth and status change still picked up).

**Inspiration**

- Cronicle `lib/job.js` `watchJobLog()`: keeps the log descriptor and on each tick
  `fs.fstat`s it, reads only `if (stats.size > log_pos)`, holds back a partial last line and
  emits all new lines in one `socket.emit('log_data', lines)`. We borrowed "size check before
  read" and "one send per batch".
- Uptime Kuma `server/model/monitor.js`: each heartbeat is pushed to the browser right where
  it is produced (`io.to(this.user_id).emit("heartbeat", …)`), with no reader polling. We
  borrowed "the producer wakes the reader", in-process with a channel. No code copied.

**Learned**

- Go's `os.Open` on Windows doesn't share delete access, so holding a log open for the
  whole stream (Cronicle keeps its fd) would block cleanup of that file; reopen per batch.
- The single-connection SQLite store makes any per-viewer timer a scheduler cost; worth
  auditing the other live polls (home, runs list) for the same pattern.

**Ideas for later**

1. Push run status changes the same way (one SSE "events" stream for home/runs instead of
   the 2–15 s list polls).
2. Ring the bell for plugin/AI-builder logs (`plugin_ai.go` reads the run log too).
3. Expose `LogWatchers()` on `/metrics` (open live views).

## 2026-10-23 (04:30): stop a run, and queued runs you can see

Category: **feature + logic correctness (runner, WebUI, agentctl)**. The last rounds were
resource usage (live log push), UX (dialog guard) and performance; this one closes a gap in
what the user can control.

**Done**

- **Runs can be stopped.** There was no way to stop a run from the WebUI (or agentctl):
  `Runner.Cancel` existed but nothing called it. Now a running or queued run has a Stop
  button on its page (next to the live state, so it's above the fold at 1024×655) and a
  stop icon in its row on the runs list (shown on hover, like rerun). Running → a short
  confirm ("Claude Code is ended right away; tokens used so far still count"), then killed
  through the existing process-group / job-object path. Queued → taken out of the line at
  once, no question (nothing to lose). `POST /api/runs/<id>/stop` (session + CSRF; 202 with
  `was`, 409 once finished, 404 unknown) and `agentctl runs stop --id <run>`.
- **A user stop is quiet.** It has its own cancel cause (`runner.ErrStoppedByUser`, stored
  as `stopped: by user`, shown as "已手动停止。"/"Stopped by you."): no "run failed" notice,
  no retry, no circuit-breaker strike, no skill-outcome record — same treatment as a quit.
- **Queued runs are real rows now.** Before, a run waiting for a free slot had no row until
  it started: invisible on the runs list and home, not stoppable, and not counted by
  `HasActiveRun`, so a frequent task with overlap "skip" could pile up several waiting runs
  while the slots were busy. The row (status `queued`) is now written before waiting; the
  start answer carries its id (and `queue_pos`), so "Run now" opens the queued run directly.
- **Place in line.** The concurrency limit is a small FIFO (`runner/queue.go`) instead of a
  buffered channel, so it knows who waits and in which order: pill "排队中 · 第 2 位" /
  "Queued · #2" (tooltip: starts on its own, timeout counts from the start), the start toast
  says "已排队（第 2 位）", the run page shows "等待中…" and redraws once when the run gets
  its slot. The log gets a first line `queued: waiting for a free slot (N ahead)`.
- **Timeout starts with the run**, not with the wait (it did before too; kept and tested).
  A queued run stopped by the user, the quit, or its caller ends `canceled` with no start
  time, no duration ("未开始") and no cost, and its log ends with `[[end]]`.
- Small: the "running" filter on the runs list includes queued runs; runs created in the
  same second sort by id (ms-ordered), so queued runs list in their real order.
- Same on every OS: no new OS calls; the stop reuses the existing kill path (process group on
  macOS/Linux, job object on Windows). The slot queue tests are pure Go and run on Windows CI.
- Tests: `TestSlotsFIFOAndLeave`, `TestSlotsGrantedWhileLeaving` (slot handed over while the
  waiter gives up is passed on, 200 rounds), `TestQueuedRunVisibleStoppableAndTimedFromStart`,
  updated `TestDrainRefusesNewAndQueuedRuns` (queued row closed by the quit) and
  `TestUserCancelIsNotQuit`; `TestUserStopQueuedAndRunning` (app: queue pos, stop both, no
  failed event, no retry with RetryMax 3, no breaker count); `TestStopRunAndQueuePos`
  (API codes, CSRF, filter, start answer), `TestStopRunUI` (wiring + i18n keys).

**Inspiration**

- n8n `packages/cli/src/executions/execution.service.ts` `stopInRegularMode()`: if the
  execution is still in the concurrency queue (`concurrencyControl.has(id)`) it is removed
  from the queue and closed with `stopBeforeRun()`, otherwise the active execution is
  cancelled with a `ManualExecutionCancelledError`; the final write is guarded by the
  expected status. `packages/cli/src/concurrency/concurrency-queue.ts`: the queue is an
  ordered array of `{ executionId, resolve }`, with `remove(id)` and `has(id)`, and the
  execution row exists before it is throttled. We borrowed "persist first, then wait in a
  named FIFO; stop = leave the line or cancel, with a distinct manual-cancel cause". No code
  copied.

**Learned**

- A buffered channel as a semaphore is FIFO in practice but can't say who is waiting or let
  one leave by id; a 60-line mutex + slice queue does both and is easy to test without
  processes.
- The scheduler's overlap check reads the database, so anything that waits must have a row,
  or the policy is silently weaker than it looks.

**Ideas for later**

1. Re-check budgets when a queued run gets its slot (today they're checked when it is
   queued; a long queue could pass the daily cap).
2. "Stop all" / "clear queue" on the runs page header when several are queued.
3. Stop from the home "running now" list and the tray menu (needs a small confirm there).
4. Let the user move a queued run to the front.

README screenshots: the runs list looks the same unless runs are queued; the run page of a
live run now has a Stop button in the log header (the README shows a finished run).

## 2026-10-23 (06:20): queued runs are checked again when they get their slot

Category: **logic correctness (budget, scheduler, runner)**. The last rounds were a feature
(stop / queue), resource usage (live log push) and UX (dialog guard); this one closes a gap
the queue opened, listed as idea 1 of the 04:30 round.

**Done**

- **Budgets are re-checked at the slot, not only at the door.** The daily caps (global,
  bucket, task) and the provider quota were checked once, when a run was asked for. A run
  that then waited in line kept that answer: three queued runs could each start after the
  runs ahead of them had used up the day's budget, and each one's per-run cap was "what was
  left when I queued", so together they could spend several times what was left.
- Now a run that had to wait gets a second check right before Claude Code starts
  (`runner.Options.BeforeStart`, called only if the run waited, so a run that got a slot at
  once costs nothing extra). The check is the same code as the first one, moved into
  `App.admitRun` (caps, quota, breach log, notice, pause per the budget's action). If it says
  no, the run ends as **skipped** — never started, no start time, no cost — and the slot goes
  straight to the next in line. If it says yes, the run starts with the fresh per-run cap
  (cost control and `--max-budget-usd`), so a smaller remainder is respected.
- **A scheduled run of a task paused while it waited is skipped too** (`skipped: task paused`).
  Runs started by hand (Run now, buttons, quick runs) still go, as before on paused tasks.
  This skip is quiet: no retry, no circuit-breaker strike, no notice.
- A budget skip sends one budget notice (the breach), not an extra "run failed" one, and it
  isn't retried (budget errors never are). Skill canary metrics ignore skipped runs.
- WebUI: the reason is shown in the viewer's language ("未启动：排队期间预算或额度已用完。
  全局：已达每日 token 上限（61572 / 1000）……"), reusing the spend page's `be.*` texts via a
  small `budgetWhy()`; and the status pill of a skipped or stopped-before-start run carries
  the reason as its tooltip, on the run page and in the runs list, so it's readable without
  scrolling at 1024×655 (the details block is below the log).
- Same on every OS: no new OS calls; pure Go hook + strings. Runner test runs on unix (mock
  is a shell script, like the other queue tests); the slot logic itself is already covered on
  Windows by the pure-Go queue tests.
- Tests: `TestBeforeStartRecheckOnSlot` (no check without a wait; "no" → skipped row, log
  `[[end]]`, slot passed on; "yes" can tighten options — a shorter timeout takes effect),
  `TestQueuedRunSkippedWhenBudgetRanOut` (cron + quick run skipped after spend landed, task
  paused per `pause_notify`, 2 budget notices, no `run_failed`, no retry),
  `TestQueuedCronRunSkippedWhenTaskPaused` (cron skipped, breaker 0, manual run on the same
  paused task still starts), `TestSkippedRunUI` (wiring + i18n keys).

**Inspiration**

- n8n `packages/cli/src/active-executions.ts` `add()`: after
  `capacityReservation.reserve()` (the concurrency wait) it claims the execution only
  `{ requireStatus: existingExecution.expectedStatus }` — i.e. it re-validates what may have
  changed during the wait — and on any throw releases the reserved capacity
  (`capacityReservation.release()`), see also `concurrency/concurrency-capacity-reservation.ts`.
  We borrowed "re-check after the wait, and hand the slot back if the re-check fails".
  No code copied.

**Learned**

- Checking a limit before a wait and acting after it is a classic TOCTOU; the queue made the
  gap minutes long. Anything else decided at queue time (provider choice, skill version,
  amendments marked "next run") is worth a look with the same eye.
- Two runs that start together can still both get "what's left" (spend lands only as they
  go, every 5 s). Exact enforcement would need a reservation (hold the per-run cap against
  the day's remainder while a run is live).

**Ideas for later**

1. Budget reservations: subtract live runs' caps from the remainder at check time, so two
   concurrent runs can't each be allowed the whole remainder.
2. Re-resolve the task's provider / prompt version at the slot (a task edited while queued
   still runs the version compiled at queue time).
3. Runs list filter chip for "skipped", and a home-page line "N runs skipped by budget today".

README screenshots are unaffected (runs list and run page look the same unless a run was
skipped). Screenshots of the new state: `/workspace/shots/run-skipped-budget-1024x655.png`,
`runs-list-skipped-tip-1024x655.png` (+ `-en`).

## 2026-10-23 (08:20): prompt drafts survive a closed dialog or a reload

Category: **UX / frontend interaction (quick run, new task)**. The last rounds were runner and
backend work (queue budget re-check, stop/queue, live log push); this one is in the dialogs,
idea 1 of the 00:20 round.

**Done**

- **A half-written prompt is no longer lost.** In a fresh quick run or new task, the prompt is
  kept in `sessionStorage` (`tb_draft_qr`, `tb_draft_task`) 400 ms after you stop typing, and
  right away when the dialog closes (any way: Discard, Back, route change) or the page hides.
  Opening the dialog again — or after a reload, a crash of the tab, or an expired session and
  a new sign-in — puts it back, with a small pill "已恢复草稿 / Draft restored" and a "清空 /
  Clear" link at the right end of the prompt label row. When and where it was saved is in the
  pill's tooltip (secondary meta info stays out of the layout). The row has a fixed 24 px
  height, so the pill appearing or going doesn't move anything.
- New task: the restored prompt goes through the usual auto-fill (name, schedule, budget are
  inferred again), so nothing else has to be kept.
- A successful run / create removes the draft; emptying the field removes it; "Clear" empties
  the field, drops the draft and puts the cursor back. A restored draft doesn't count as an
  edit for the discard guard (closing without touching it closes at once — the draft stays).
- Not used where it would get in the way: edit & rerun, edit task and "save as task" (they
  start from a given prompt) neither restore nor save drafts.
- Privacy: `sessionStorage` only — the draft lives in this window and is gone when it closes;
  sign-out and Settings → "reset UI preferences" remove all `tb_draft_*` keys. Drafts older than
  7 days, blank, over 32 000 chars or malformed are ignored. Nothing goes to the server.
- Same on every OS: `sessionStorage`, `pagehide` and `visibilitychange` behave the same in
  WKWebView (macOS), WebView2 (Windows), WebKitGTK (Linux) and browsers; text is restored
  byte for byte (CRLF from a Windows paste included).
- Verified in headless Chrome at 1024×655 on the demo data (zh-CN and en): type → Esc →
  Discard → reopen restores; reload restores; Esc right after typing still saves (flush on
  close); Ctrl+Enter run clears; new task restores and re-infers the name; footer stays
  visible. Shots: `/workspace/shots/prompt-draft-restored-1024x655.png`,
  `prompt-draft-task-1024x655.png` (+ `-en-` versions).
- Tests: `TestPromptDrafts` (wiring: quick run / new task only, cleared on submit, sign-out,
  flush on close and route change, sessionStorage not localStorage, i18n keys in both
  languages, CSS; under node: CRLF round trip, TTL, blank / over-long / corrupt drafts
  ignored, flush runs once, clear cancels a pending save, clear-all keeps other keys).

**Inspiration**

- Open WebUI `src/lib/components/chat/Chat.svelte`: `saveDraft()` writes the input to
  `sessionStorage` under `chat-input[-<chatId>]` with a 500 ms debounce (or at once before an
  OAuth redirect), skips it above `MAX_DRAFT_LENGTH`, removes it when the text is empty;
  `restoreChatInput()` parses it back when the chat opens and falls back to defaults on a parse
  error; `clearDraft()` runs after sending. `MessageInput.svelte` feeds every change through
  `onChange(chatInputDraft)`. We borrowed "debounced per-window draft, restored on open,
  cleared on send", and added a visible "restored · clear" pill and a flush on close.
  No code copied.

**Learned**

- Chrome doesn't fire `blur` when a focused textarea is removed with its dialog, so a debounce
  alone loses the last 400 ms of typing on Esc; the save has to be flushed by the code that
  removes the dialog (modal close and the router).
- `sessionStorage` survives reload and a session-expiry re-login but not a closed window,
  which is the right lifetime for unsent prompts on a shared machine.

**Ideas for later**

1. Drafts for the AI plugin builder description and amendment input.
2. Keep the provider / schedule the user picked with the draft (today only the prompt).
3. Stacked dialogs (picker on top of a form), carried over from 00:20.

README screenshots are unaffected (the pill only shows when a draft exists).

## 2026-10-23 (10:20): runs list — counts on the filters, "failed" with timeouts, load more

Category: **logic correctness + UX (runs list, API)**. The last rounds were prompt drafts
(UX), the queue budget re-check and the stop/queue work (runner); this one fixes what the
runs list silently hid.

**Done**

- **The list no longer stops at 200.** It loads 100 runs at a time; below the table a 48 px
  footer says "已显示 100 / 523 · 加载更多" ("Showing 100 of 523 · Load more") and goes away
  when everything is shown. For the time sorts the next page starts after the last run shown
  (`/api/runs?before=<run id>`, a keyset cursor on `(created_at, id)`), so a run that starts
  while you read doesn't shift the pages or repeat a row; cost / tokens / duration sorts page
  by `offset` (rows deduped by id). Pages already loaded stay when the list refreshes live or
  re-renders from cache (up to 1000 rows).
- **Filters say how many.** Each chip carries its count for the same task and search text
  (`GET /api/runs/counts`, one grouped query, refreshed with the list when `/api/rev` moves).
- **"Failed" includes timeouts** — before, a timed-out run showed under no filter except All.
  New chip **"已停止 / Stopped"** for runs stopped by hand or skipped (budget / paused task):
  on purpose, not a fault, so they stay out of Failed. Each grouped chip explains itself in
  its tooltip. `?status=<exact status>` still works for links and scripts.
- **Bug: switching the status chip dropped the task filter** ("View all" from a task page,
  then "Failed", showed every task's failures). The task stays now. The task name moved from
  the toolbar's right end to the page subtitle, so search, five chips and sort fit one row at
  1024×655 in both languages (search shrinks 280 → 140 px before anything wraps).
- Same on every OS: plain SQL + DOM; numbers through `Intl.NumberFormat`, no new OS calls.
  All parameters bound (`IN (?,?)`, LIKE escape unchanged).
- Verified in headless Chrome at 1024×655 (zh-CN, en) on 249 demo runs: 100 → 200 → 249,
  no duplicates, footer hides at the end, toolbar 57 px (one row), no page errors.
  Shots: `/workspace/shots/runs-filter-counts-1024x655.png`, `runs-load-more-1024x655.png`
  (+ `-en-`).
- Tests: `TestRunsFilterGroupsAndCounts` (groups, exact status, counts with search / task,
  injection text counted as text, POST 405, long search 400), `TestRunsPaging` (keyset pages
  across same-second runs and a run inserted between pages, `sort=old`, offset for cost,
  `before` refused with cost sort / unknown id, cursor + filter), `TestRunsPageUI` (wiring,
  CSS, i18n keys + placeholders).

**Inspiration**

- n8n `packages/cli/src/executions/execution.service.ts` `findManyAndCount()`: pages with a
  `lastId` cursor and returns the matching `count` alongside, so the list can show "x of y"
  and a load-more. n8n `packages/@n8n/db/src/repositories/execution.repository.ts`: `error`
  and `crashed` are both failures, while `canceled` is "somebody stopping a run on purpose,
  which is not a fault" — the same split as our Failed vs Stopped. No code copied.

**Learned**

- A status filter that maps 1:1 to DB values hides whole states the moment a new status is
  added (timeout, skipped). Filters should be user-facing groups.
- The SWR re-render reruns the route, so per-page state (pages loaded) has to live in the
  view object keyed by the filter, or a background refresh throws "Load more" away.

**Ideas for later**

1. Date range on the runs list ("today / 7 days / custom"), reusing the keyset cursor.
2. Select several runs → rerun / export / mark important.
3. Home page line "N runs skipped by budget today" linking to `#/runs?status=stopped`.

## 2026-10-23 (16:00): macOS desktop "Update" button did nothing

Category: **bug fix (desktop, macOS)**. Committed as `597e1d6`; this entry was missing and is
added by the next run.

**Done**

- The update confirmation used `window.confirm()`, which WKWebView answers with `false`
  without showing anything, so "Update" silently did nothing on macOS. It is an in-page dialog
  now, and a test forbids native `confirm/alert/prompt` in app.js.
- Checking and updating run as a background job the UI polls: checking → up to date (check
  time in the tooltip) → new version → download progress → installing → restart, with retry on
  error. Each source times out after 15 s, the whole check after 45 s; a download with no data
  for 30 s is aborted; a proxy timeout says to check the proxy node.
- Tests: `internal/app/updatejob_test.go`, `internal/updater/stall_test.go`,
  `internal/webui/nativedialog_test.go`.

**Ideas for later**

1. Show the update job in the tray menu ("Downloading 42 %").

## 2026-10-23 (18:20): JSON API answers gzipped

Category: **resource usage (network, server + browser)**. The last rounds were the runs list
and an update bug; this one is underneath every page that polls.

**Done**

- `writeJSON` now encodes into a pooled buffer and, when the client accepts gzip and the body
  is at least 1 KB, sends it gzipped (pooled `gzip.Writer`, default level). Smaller answers,
  `gzip;q=0`, `Cache-Control: no-transform` and 204/304 go out as before, now with an exact
  `Content-Length`. Every `/api/` answer carries `Vary: Accept-Encoding`.
- Only JSON is compressed: the live log (SSE) stays identity and still flushes per batch,
  downloads (skill .zip, spend CSV) and the auth layer's 401/403 are untouched. The marker
  wrapper passes `Flush` and `Unwrap` through.
- **Same on every OS.** The desktop window's in-process handler already drops
  `Accept-Encoding` (WKWebView / WebView2 / WebKitGTK don't all decode it), so the desktop app
  gets identity bytes on macOS, Windows and Linux; the loopback bridge and server/browser mode
  get gzip. Pure Go `compress/gzip`, no new dependency.
- Numbers (demo agentd, 249 runs with varied prompts, curl): `/api/runs` 100 rows
  69.9 → 11.9 KB, 200 rows 140.4 → 23.0 KB; `/api/overview` 6.4 → 1.2 KB, `/api/providers`
  9.5 → 1.6 KB, `/api/settings` 2.9 → 0.7 KB; `/api/me` (323 B) and `/api/rev` unchanged.
  Response time on loopback the same (≈4 ms). Buffers above 256 KB are not kept in the pool.
- Verified in headless Chrome at 1024×655: runs page renders 100 rows, `overview`,
  `dashboard`, `runs` arrive gzipped, no page errors. Shot: `/workspace/shots/api-gzip-runs-1024x655.png`.
- Tests: `TestAPIJSONGzip` (gzip body decodes to the identity body, ≥3× smaller, identity has
  exact Content-Length, Vary on both, q=0, small answer, 400 error, index and 401 untouched),
  `TestDesktopAPIIdentity`, `TestSSENotGzipped` (real listener, gzip client, event arrives
  uncompressed), `TestWriteJSONStatusAndPool` (204, >256 KB answer, pool bound, no-transform).

**Inspiration**

- n8n `packages/cli/src/abstract-server.ts` `setupCommonMiddlewares()`: one `compression()`
  middleware in front of the whole REST API; expressjs/compression `index.js` only compresses
  above a 1 KB `threshold`, for compressible types, and honours `Cache-Control: no-transform`.
- Open WebUI `backend/open_webui/main.py`: `CompressMiddleware` (starlette-compress) added
  outermost, behind the `ENABLE_COMPRESSION_MIDDLEWARE` switch.
- We borrowed "threshold + no-transform + Vary", but compress only in our single JSON writer
  instead of a generic middleware, so streaming and downloads can't be buffered by mistake.
  No code copied.

**Learned**

- Go's `http.Client` adds `Accept-Encoding: gzip` and decodes on its own, so the existing SSE
  tests already ran through the gzip path; a test that wants to see the raw encoding has to
  set the header itself.
- Identical seeded rows compress 20×+ and flatter the result; measure with varied text.

**Ideas for later**

1. Gzip the spend CSV export and skill file listings the same way (needs the download path
   to keep `Content-Disposition` and size hints right).
2. `ETag` on the heavier GET answers (overview, providers) so an unchanged poll is a 304.
3. Brotli for static files, carried over from 22:20.

## 2026-10-23 (20:20): task page shows the real working folder — copy it, open it in Finder / Explorer

Category: **feature + cross-platform (task detail, desktop app)**. The last rounds were API gzip
(resource), the macOS update bug and the runs list; this one picks up idea 1 from 10:13.

**Done**

- **Bug: a task without its own folder showed "—"** under Workspace, although its runs work in
  `<home>/workspaces/<task id>`. The detail answer (`GET /api/tasks/{id}`) now returns the folder
  runs really use — the task's own, or the per-task default (also when the spec names the data
  folder, same rule as `app.prepareGuard`) — plus `workspace_default`. The list answer is unchanged.
- The path sits in a code chip that wraps a long path; a **"默认 / Default"** tag explains in its
  tooltip that tidebot keeps this folder for the task. **Copy path** button everywhere.
- **Desktop app: open the folder** (button, or `o` on the task page). It is named after the OS
  file manager, like VS Code: "在访达中打开 / Open in Finder" (macOS), "在资源管理器中打开 /
  Open in File Explorer" (Windows), "在文件管理器中打开 / Open in file manager" (Linux) —
  `GET /api/desktop/open` now also returns `os`. Hidden in the browser / server mode (no file
  manager there), and `o` does nothing when the button is hidden.
- `POST /api/desktop/open {"target":"workspace","id":<task id>}`: the server looks the folder up
  from the task, never from the page. The default folder is created first (it would be on the
  next run anyway); a task's own folder is never created by the UI — missing → 404 → "文件夹还
  不存在，任务第一次运行时会创建". Unknown / empty / path-looking ids are 404 and never reach
  the file manager.
- **macOS safety**: `open X.app` launches a bundle instead of showing it. `osopen` now shows any
  folder whose name has an extension (`.app`, `.bundle`, …) selected in its parent (`open -R`).
- Layout at 1024×655 (zh-CN, en): one 28 px row on the 8 px grid, path text level with the
  label, tag and buttons centred on the first line, tooltips anchored right so nothing overflows
  (scrollWidth stays 1024). Shots: `/workspace/tidebot-shots/task-workspace-open-1024x655.png`,
  `task-workspace-default-tip-1024x655.png` (+ `-en-`).
- Tests: `TestTaskWorkspaceShownAndOpened` (default path + flag, list untouched, `os` in GET,
  501 in browser, default created on open, own folder shown/opened but not created, data dir
  counts as default, bad ids never open), `TestTaskWorkspaceUI` (wiring, CSS, i18n keys,
  shortcut help), `TestCommands` (bundle folders revealed on macOS, plain folders opened).

**Inspiration**

- farion1231/cc-switch `src-tauri/src/commands/config.rs` `open_config_folder()`: the page names
  *which* folder (an app type), the backend resolves the path, creates it if missing, then opens it.
- microsoft/vscode `src/vs/workbench/contrib/files/electron-browser/fileActions.contribution.ts`:
  `REVEAL_IN_OS_LABEL` picks "Reveal in File Explorer / Finder / Open Containing Folder" by OS.
  No code copied.

**Learned**

- `[data-tip]` tooltips are centred by default; near the right edge of a 1024 px window a 280 px
  tooltip widens the page by ~36 px. Rows that end at the card's right edge need the right-anchored
  variant.
- The WebUI form can't set a task's folder (only `agentctl tasks create --workspace`), so the
  tooltip must not send people to "Edit" for it.

**Ideas for later**

1. Let the task form pick / type a working folder (desktop: native folder picker), with the default
   shown as placeholder.
2. "Open folder" on a run's detail page (runs keep their `workspace_dir`).
3. Show the folder's size and file count in the tooltip (reuse the storage scanner).

## 2026-10-23 (22:20): task form sets the working folder — type it, or pick it in the desktop app

Category: **feature + cross-platform (task form, desktop app)**. Idea 1 from 20:20: until now only
`agentctl tasks create --workspace` could give a task its own folder.

**Done**

- New **"工作文件夹 / Working folder"** field in the new / edit task dialog. Empty = the folder
  tidebot keeps for the task, shown as the placeholder ("默认：…/workspaces/<task id>"; a new task
  shows `…/workspaces/…`, its id doesn't exist yet). The field's ⓘ explains the rest.
- **Desktop app: "选择文件夹… / Choose folder…"** opens the native folder dialog (Wails
  `CanChooseDirectories`: NSOpenPanel / IFileOpenDialog pick-folders / GTK select-folder), starting
  at the current folder. Windows reports Cancel as an error; it is treated as cancel. Hidden in the
  browser / server mode (`GET /api/desktop/pick-folder` → `available:false`, POST is 501).
- **"改回默认 / Use the default folder"** icon clears the field (only shown when it has a value).
- The server cleans what was typed: surrounding quotes (Explorer's "Copy as path"), `~` / `~/x` /
  `~\x`, trailing separators, OS separators. Refused: relative paths, a drive / file system root,
  anything inside tidebot's home (keys, database), an existing file, > 1024 bytes. The data folder
  or the task's own default folder count as "default". Same check backs a live
  `GET /api/tasks/workspace-check`, so the form shows a small pill in the label row while typing —
  "将新建 / New" (created on first run) or "不可用 / Can't use" with the reason in its tooltip — and
  Save is blocked with that reason. The pill sits in the label row, so nothing below moves.
- `PUT /api/tasks/{id}` without `workspace` (an older open page) keeps the folder; `""` clears it.
  `tasksvc.UpdateSpec` now always syncs `tasks.workspace_dir` from the spec (like `Rollback`), so
  clearing really sends runs back to the default folder.
- Layout at 1024×655 (zh-CN, en): 36 px input with 28 px icon buttons centred on it (8 px gaps),
  tooltips in the label row anchored left so the modal doesn't clip them, `scrollWidth` 1024.
  Shots: `/workspace/tidebot-shots/task-form-workspace-picked-1024x655.png`,
  `task-form-workspace-default-1024x655.png`, `task-form-workspace-bad-1024x655.png` (+ `-en-`).
- Tests: `TestTaskFormWorkspace` (create with a quoted path, form round trip, edit without the
  field keeps it, bad paths refused and nothing changed, clear → default, default / data folder →
  default, `~`), `TestWorkspaceCheck`, `TestPickFolder` (browser 501, start only when an existing
  folder, cancel), `TestTaskFormWorkspaceUI` (wiring, CSS, i18n keys).

**Inspiration**

- farion1231/cc-switch `src/components/settings/DirectorySettings.tsx` and
  `src/hooks/useDirectorySettings.ts`: one row per folder — input with the resolved default,
  a browse icon (`selectConfigDirectory(currentValue)` opens at the current folder) and a
  reset-to-default icon, both with hover tips; picked / typed values are trimmed and empty means
  default (`sanitizeDir`). No code copied.

**Learned**

- `[data-tip]` tooltips are centred; in a modal's left column they are clipped by the modal's
  overflow. Label-row tips there need the left-anchored variant.
- Wails v3 beta.30 returns `cancelled by user` as an error on Windows only; macOS / Linux return "".
- The modal's unsaved-changes guard only counts trusted input events, so values set from the folder
  dialog or the reset icon must mark the field touched by hand.

**Ideas for later**

1. Remember recently used folders and offer them as suggestions (`<datalist>`), like VS Code's
   "Open Recent".
2. Drag a folder from Finder / Explorer onto the field to fill it (desktop app gets the path).
3. Warn (not refuse) when the folder is the whole home folder, since the agent may write anywhere in it.

## 2026-10-24 (00:20): change the password in the WebUI, sign out other sessions

Category: **security + "everything in the WebUI"** (Settings → Security). The last two rounds were
features for the task folder; before that resource usage. Until now the password could only be
changed with `agentctl auth set-password`, and there was no way to end a login left open on
another computer.

**Done**

- Security → Password row: "已设置 / Set" pill plus a **"修改密码 / Change password"** button
  ("设置密码 / Set password" and a "未设置" pill when a desktop window runs without one). The row
  text keeps the CLI as the forgot-it path; the command chip no longer breaks mid-word.
- Dialog: current password, new password (with "显示 / Show" for both new fields), repeat. Live
  "至少 8 位 / At least 8 characters" hint that turns green, "两次输入不一样" once the repeat is as
  long as the new one, "和当前密码一样" on submit. Enter submits. Inputs carry
  `current-password` / `new-password` plus a hidden `username` so password managers fill / save
  them. Fits 1024×655 (modal 520×474, footer visible, scrollWidth 1024).
- `POST /api/auth/password {current,new}` (session + CSRF, 4 KB body cap): `auth.ChangePassword`
  checks the current password (not needed only when none is set yet), 8–1024 bytes, not the same;
  writes the argon2id hash and **deletes every other session in the same transaction** — this
  window stays. Answer: `{signed_out:n}` → toast "密码已更新，已退出其他 n 处登录". Errors carry a
  `code` (`bad_current` 403, `length` / `same` 400). Nothing echoes or logs a password.
- **Wrong current passwords count as failed logins** for that IP (`login_attempts`), so a stolen
  session can't brute-force the password: after 5 the endpoint answers 429 + `Retry-After` and the
  dialog counts down on its submit button.
- Session row: **"退出其他登录（n）/ Sign out others (n)"** when other live sessions exist
  (`/api/settings` now returns `password_set`, `other_sessions`; `POST /api/auth/sessions/others`).
  What "others" means is in the button's tooltip.
- Same on macOS / Windows / Linux: plain WebUI + Go, no OS paths; the desktop window's local
  session counts as "this window" and is kept.
- Shots: `/workspace/tidebot-shots/password-change-zh-CN-1024x655.png`,
  `security-row-zh-CN-1024x655.png` (+ `-en-`).
- Tests: `TestPasswordChange` (CSRF needed, bad current / short / too long / same, success keeps
  this session and ends the other, old password refused, new one works, GET 405),
  `TestPasswordChangeRateLimited` (5 wrong guesses → 429 even with the right one, nothing
  changed), `TestPasswordFirstSetAndSignOutOthers`, `TestPasswordUI` (wiring, CSS, i18n keys).

**Inspiration**

- louislam/uptime-kuma `src/components/settings/Security.vue`: current / new / repeat fields with
  `autocomplete` hints, mismatch shown on the repeat field, fields cleared after success.
- open-webui/open-webui `backend/open_webui/routers/auths.py` `update_password()` (re-verify the
  current password, validate, hash, then `disconnect_user_sessions`) and
  `src/lib/components/chat/Settings/Account/UpdatePassword.svelte`. We keep the current window
  signed in instead of signing everyone out. No code copied.

**Learned**

- `.modal form` sets a 16 px flex gap, which beats a single-class rule on the form; reserved error
  lines then doubled the spacing. Scope dialog layout rules as `.modal .x`.
- A re-check of the password inside a signed-in session is a second login form and needs the same
  rate limit; `DisableTOTPChecked` (2FA off) still has none.

**Ideas for later**

1. Rate-limit the "turn off 2FA" password check the same way (share `checkRateLimit`).
2. List sessions (created, last seen, IP, "this window") with a per-row sign-out, like GitHub's
   sessions page.
3. Have `agentctl auth set-password` (the forgot-password reset) sign out all browser sessions too,
   once the desktop window re-creates its local session on 401.

## 2026-10-24 (02:20): task list shows the last 10 runs, not just the last status

Category: **UI polish + small logic fix** (task list). The last rounds were security (password),
the task form and the task folder; this one is the list people look at most.

**Done**

- The "上次运行 / Last run" column is now **"最近运行 / Recent runs"**: a strip of the task's last
  10 runs (6×16 px cells on a 2 px gap, oldest left, empty slots in sand for newer tasks; green
  succeeded, red failed / timeout, amber skipped, blue running — pulsing, paused while the tab is
  hidden and off with reduced motion). Each cell links to its run; hover shows time · status ·
  duration · cost, anchored right so it stays inside the window. Under it: when the newest run
  started (live "x 分钟前", full time in the tooltip) and **"成功 8/10 / 8/10 OK"**, red when any
  failed. Stopped and budget-skipped runs don't count as failures (said in the tooltip). No runs:
  "从未 / Never". Sorting by the column sorts by the newest run's time instead of the status word.
- The task name column now takes the space that's left and ellipsizes (`width:100%; max-width:0`),
  so the wider cell never pushes the table past the card at 1024×655 (scrollWidth 1024 in zh-CN and
  en; it was 1033 before the fix while building this).
- **Bug: "今日运行 / Runs today" counted the UTC date.** In Asia/Shanghai a run at 07:00 counted for
  "yesterday". It now counts from local midnight in the display zone, like the home page KPIs.
- **Fewer queries:** `GET /api/tasks` filled last status and runs today with two queries per task;
  it now uses two queries for the whole list (one `ROW_NUMBER() … PARTITION BY task_id` window on
  `idx_runs_task_created`, one `GROUP BY task_id`). New list fields `last_run_at` and `beats`
  (omitted when empty); the task detail answer is unchanged.
- Same on macOS / Windows / Linux, desktop window and browser: plain HTML/CSS cells, no fonts or
  paths involved, `Ctrl`/`⌘` not used.
- Shots: `/workspace/tidebot-shots/task-list-recent-runs-1024x655.png`,
  `task-list-recent-runs-tip-1024x655.png`, `task-list-recent-runs-en-1024x655.png`.
- Tests: `TestTaskListRecentRuns` (newest 10 oldest first, last status/time, local-day runs today
  across midnight in UTC+14, archived task left out, idle task empty, detail answer untouched),
  `TestTaskListRecentRunsUI` (wiring, CSS, i18n keys).

**Inspiration**

- louislam/uptime-kuma `src/components/MonitorListItem.vue`: every monitor row in the list carries
  `<HeartbeatBar size="small">` next to an uptime pill, so a glance at the list shows which one is
  flaky. `src/components/HeartbeatBar.vue`: fixed number of slots with empty placeholders, narrower
  beats for `size !== "big"`, a title per beat. We use a fixed 10 slots (no resize logic) and put
  the success count under the strip. No code copied.

**Learned**

- A `small.clamp1 { max-width: 240px }` in the name column made the table's minimum width rigid;
  any wider column elsewhere overflowed the card. Letting the flexible column have `max-width: 0`
  is the usual table trick and keeps the ellipsis.
- A "today" built with `time.Now().UTC().Format("2006-01-02")` is wrong for 8 hours a day in the
  user's zone. This was the last one in non-test code; day boundaries go through `s.loc()`.

**Ideas for later**

1. Same small strip on the skills list (runs that used each skill) and the task detail header.
2. Filter the task list by "failing" (last run failed / timed out), with a count like the runs list.
3. Click the "成功 8/10" text to open the runs list filtered to that task.
