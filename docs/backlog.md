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
