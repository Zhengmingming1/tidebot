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

## 2026-10-24 (04:20): 2FA codes work once, turning 2FA off is rate-limited

Category: **security / correctness** (sign-in and Settings → Security). The last rounds were UI and
features; this closes two gaps in two-factor sign-in, one of them idea 1 from the 00:20 entry.

**Done**

- **A 6-digit code is accepted once.** Before, any code was valid for its whole ±30 s window, so a
  code seen over a shoulder or in a proxy log could sign in again within about 90 s. Now
  `users.totp_last_step` (new column, added idempotently) keeps the time step of the last accepted
  code; sign-in only accepts a code from a **later** step (RFC 6238 §5.2). The check is one
  conditional `UPDATE … WHERE totp_last_step < ?`, so two sign-ins racing with one code can't both
  get in. A refused reuse is logged in `login_attempts` as `totp_reused` and counts toward the
  lockout. Recovery codes were already single-use.
- **The code that turns 2FA on is spent too** (WebUI confirm and `agentctl auth totp-setup`, now
  via `EnableTOTPWithCode`), so it can't be replayed for a sign-in. Code comparison is constant-time.
- **Turning 2FA off is rate-limited like sign-in:** wrong passwords count as failed logins for that
  IP; after 5 the endpoint answers 429 + `Retry-After` even with the right one. Errors carry
  `code: bad_password`; other failures are a 500 instead of a 403 that looked like a wrong password.
  With no password set (desktop window) no password is asked.
- `agentctl auth totp-disable` now also clears the recovery codes, so a later setup doesn't carry
  old ones.
- Dialog: a real `<form>` (Enter submits, hidden `username` + `current-password` for password
  managers), "请输入密码确认" when empty, "密码不对" only for a wrong password, a countdown on the
  disabled button while locked out, no password field when none is set. Sign-in's wrong-code
  message now says a code works only once. zh-CN and en.
- Same on macOS / Windows / Linux, desktop window and browser: Go + plain HTML, no paths or keys
  involved. Modal 520×291 at 1024×655, scrollWidth 1024.
- Shots: `/workspace/tidebot-shots/2fa-off-wrong-zh-CN-1024x655.png`,
  `2fa-off-locked-zh-CN-1024x655.png`, `2fa-off-wrong-en-1024x655.png`.
- Tests: `TestTOTPCodeUsedOnce` (setup code refused for sign-in, next code once, older code refused
  after a newer one, `totp_reused` logged, disable clears marker and recovery codes),
  `TestTOTPConcurrentReuse` (6 parallel sign-ins with one code → exactly 1), `TestTOTPStepWindow`,
  `TestDisableTOTPCheckedRateLimit` (lockout shared with sign-in, per IP),
  `TestTwoFADisableRateLimited` (HTTP 429 + Retry-After, 2FA stays on), `TestTwoFADisableUI`;
  `TestTwoFAThreeStepFlow` updated for one-time codes.

**Inspiration**

- dani-garcia/vaultwarden `src/api/core/two_factor/authenticator.rs` `validate_totp_code()`: loops
  over the ±1 step window and accepts a match only when `time_step > twofactor.last_used`, then
  saves that step; a match at or below it is logged as "already been used". Storing the step (not
  the code, as go-gitea/gitea `models/auth/twofactor.go` `LastUsedPasscode` does) also refuses older
  codes still inside the window. No code copied.

**Learned**

- Tests that built TOTP codes with `time.Now()` at each call could straddle a 30 s boundary; with
  one-time codes that turns into a flaky "replay accepted". Take one `t0` and derive every code from it.
- A 2FA-off check that answers 403 for every error made a database error look like a wrong password.

**Ideas for later**

1. Ask for a current 2FA code (or a recovery code) when turning 2FA off, not only the password.
2. Show the lockout wait as "14:57" instead of "897 秒" in the password and 2FA dialogs.
3. Rate-limit the 2FA setup confirm step and show "codes left" with a regenerate button.

## 2026-10-24 (06:20): SQLite syncs at checkpoints, not on every write

Category: **resource usage (disk I/O, core)**. The last rounds were security (2FA, password) and
the task list UI; this one is invisible but touches every write tidebot makes.

**Done**

- `db.Open` now sets `synchronous(NORMAL)` on every connection. In WAL mode the default (`FULL`)
  fsyncs on each commit; tidebot commits a lot of small rows (usage every 5 s per run, log offsets,
  session last-seen, scheduler state). Measured on the box with 1000 one-row commits: 0.19 ms →
  0.02 ms per commit, and the 1000+ `fsync` calls drop to the few done at checkpoints. Less SSD
  wear and fewer wake-ups on laptops. The trade-off: after an OS crash or power cut the last
  commits before it may roll back; the file never corrupts and an app crash loses nothing.
- `journal_size_limit(8 MiB)`: after a checkpoint the `-wal` file is cut back instead of keeping
  its peak size (it grows past 4 MiB when a long read blocks checkpoints, e.g. a backup).
- Query-planner stats: `PRAGMA optimize=0x10002` (with `analysis_limit=400`) once at open, plain
  `PRAGMA optimize` every 6 h with the retention tick and before the app closes, as the SQLite
  docs suggest for long-lived connections. No-op when tables didn't change much.
- Same on macOS / Windows / Linux, desktop and server: pure-Go SQLite (modernc), DSN pragmas only,
  no paths. No UI change, so no screenshot.
- Tests: `TestOpenConnectionPragmas` (WAL, synchronous=1, foreign keys, busy timeout, size limit —
  also on a connection opened later by the pool), `TestWALTrimmedAndDurable` (a held reader grows
  the WAL past 12 MiB; after the checkpoint it's ≤ 8 MiB; rows survive close + reopen). The size
  test fails without the new pragma.

**Inspiration**

- louislam/uptime-kuma `server/database.js` `initSQLite()`: WAL + `synchronous = NORMAL` with the
  comment that FULL is safer but slower; `close()` checkpoints before closing.
- pocketbase/pocketbase `core/db_connect.go` `DefaultDBConnect()`: per-connection `_pragma=` DSN
  with `busy_timeout` first, `journal_size_limit`, `synchronous(NORMAL)` on modernc.org/sqlite.
  No code copied.

**Learned**

- `journal_size_limit` only bites when the WAL is reset, i.e. on the first write after a complete
  checkpoint; a checkpoint alone leaves the file at full size.
- With `SetMaxOpenConns(1)` a pragma set by `db.Exec` after open would be lost if the pool ever
  reopened the connection; DSN `_pragma=` is the safe place.

**Ideas for later**

1. Show WAL size next to the database size in Storage (tooltip), and checkpoint(TRUNCATE) on
   "Shrink" (already done) — also after a backup finishes.
2. `cache_size`: measure RSS with the default 2 MiB vs 8 MiB on a 100 MB database before changing.
3. Batch the per-run 5 s usage writes of concurrent runs into one transaction.

## 2026-10-24 (08:20): task list — "failing" filter, counts on every chip, rate opens the runs

Category: **UX / navigation** (task list). The last rounds were disk I/O, 2FA and the task list's
run strip; this builds on the strip (ideas 2 and 3 from 02:20) so a broken task is one click away.

**Done**

- New chip **"失败中 / Failing"**: tasks whose newest run *with a result* failed or timed out.
  Running, queued, stopped and budget-skipped runs are passed over, so a task that is just running
  again, or was stopped by hand, keeps the verdict of its last real result. Worked out on the
  server (`failStreak` over the 10 runs the list already loads, no new query): list fields
  `failing` and `fail_streak` (omitted when false / 0).
- **Counts on every chip** ("全部 5 · 已启用 5 · 已暂停 0 · 失败中 2"); like the runs list they follow
  the search box and skill filter, not the chip itself. The failing count is red when above 0.
- The filter is **in the address** (`#/tasks?status=failing`): reload, back/forward and links open
  the same view. Changing the chip uses `history.replaceState`, so no hashchange, refetch or
  repaint. Unknown values fall back to "all". Plain `#/tasks` keeps the last chip (as before).
- **"成功 0/2 / 0/2 OK" is now a link** to that task's runs (`#/runs?task=…`); its tooltip adds
  "已连续失败 2 次 / Failed 2 times in a row" when failing.
- Empty failing view says so in a good way ("没有失败中的任务 · 每个任务最近一次有结果的运行都成功了").
- Command palette: "失败中的任务 / Failing tasks".
- Same on macOS / Windows / Linux, desktop window and browser: plain HTML/CSS, hash routes, no
  paths or modifier keys. 1024×655: chips + skill select + count fit one row in zh-CN and en,
  scrollWidth 1024.
- Shots: `/workspace/tidebot-shots/task-list-failing-1024x655.png`, `task-list-failing-tip-1024x655.png`,
  `task-list-failing-en-1024x655.png`, `task-list-failing-tip-en-1024x655.png`.
- Tests: `TestFailStreak` (table: passes over running/queued/canceled/skipped, stops at the first
  success), `TestTaskListRecentRuns` extended (`failing` + streak on the list answer, idle task has
  neither), `TestTaskListFailingFilterUI` (wiring, CSS, i18n keys).

**Inspiration**

- louislam/uptime-kuma `src/components/MonitorListFilter.vue`: the status filter lists Up / Down /
  Pending / Maintenance each with its count (`$root.stats.down` …) next to Running / Paused, so
  "what's broken" is a filter, not a scan. `src/components/MonitorList.vue` keeps `filterState` and
  matches `monitor.status` against it. We use chips (one row fits at 1024) instead of a dropdown,
  and put the filter in the hash. No code copied.

**Learned**

- "Failing" needs a definition that ignores runs without a result; otherwise every running or
  stopped task flips state and the chip flickers while you look at it.
- `history.replaceState` on a hash route keeps the URL honest without going through `route()`;
  the existing soft refresh then reads the same params, so live updates keep the chip.

**Ideas for later**

1. Home page health card: a "失败中 N" link to `#/tasks?status=failing` when N > 0.
2. Favicon badge / notification when a task *starts* failing (streak goes 0 → 1), not on every failure.
3. Sort by fail streak ("most broken first") and show the streak on the task detail header.

## 2026-10-24 (09:20): runs list — filter by date (quick ranges + a small calendar)

Category: **missing feature** (runs page, API). The last rounds were UX/navigation (task list),
disk I/O and security; this is idea 1 from 10-23 10:20 ("date range on the runs list").

**Done**

- Runs page head has a date button next to "新建快速运行": **全部时间 / 今天 / 昨天 / 最近 7 天 /
  最近 30 天** apply at once; a month calendar picks any span (click the first day, then the last;
  hover previews the span, future days are greyed out, today has a dot). Apply / Clear in the
  footer. Keyboard: arrows move by day / week (across months), Enter picks, Esc closes and gives
  focus back to the button. The button turns blue and shows the span ("10月12日 – 10月22日",
  "Oct 12 – Oct 22", year added when it isn't this year); the exact bounds and zone are in its
  tooltip, not on the page.
- Days are calendar days in the scheduler's zone, resolved **on the server** (`runsRange`), so the
  client and server agree on "today" and the home page "今日运行" count matches `range=today`.
  API: `GET /api/runs?range=today|yesterday|7d|30d` or `from=/to=YYYY-MM-DD` (both days included,
  either may be left out); bad values are a 400. Bounds go in as parameters
  (`created_at >= ? AND created_at < ?`; RFC3339 UTC text sorts in time order).
- The status chip counts, search, sort, keyset "load more" and the task filter all follow the range;
  the range is in the address (`#/runs?range=7d`, `#/runs?from=…&to=…`), so reload / back / links
  keep it and switching chips keeps it.
- Empty range says so ("最近 7 天没有运行") with a "查看全部时间" button.
- Home page "今日运行" card is now a link to today's runs; command palette has "今天的运行".
- New index `runs(created_at)` (migration 010): the list's newest-first order and the range no
  longer scan the whole table when no task filter is set.
- Same on macOS / Windows / Linux, desktop window and browser: no native date input (WebKitGTK
  and WKWebView differ there), plain buttons + CSS grid, Intl for month / weekday names (Monday
  first in zh-CN, Sunday in en), no modifier keys. 1024×655: popover 384×320 under the button,
  page scrollWidth 1024 in zh-CN and en.
- Shots: `/workspace/tidebot-shots/runs-range-pick-1024x655.png`, `runs-range-applied-1024x655.png`,
  `runs-range-empty-1024x655.png`, `home-runs-today-link-1024x655.png` (+ `-en` for each).
- Tests: `TestRunsRange` (table in a fixed UTC+8 zone: presets, open ends, swapped / bad dates,
  injection text, an instant that is already tomorrow in UTC+8), `TestRunsDateRangeFilter` (list,
  counts, search, status, keyset paging both ways, dashboard "today" agrees, 400s),
  `TestRunsDateRangeUI` (wiring, CSS, i18n keys).

**Inspiration**

- n8n-io/n8n `packages/frontend/editor-ui/src/features/execution/executions/components/ExecutionsFilter.vue`
  and `executions.utils.ts`: start / end date pickers in the executions filter, turned into
  `startedAfter` / `startedBefore` for the query and counted as an active filter (badge), with
  date changes debounced. We keep whole days, resolve them on the server in the scheduler's zone,
  add one-click quick ranges and keep the range in the hash. No code copied.

**Learned**

- Letting the client send instants would make "today" depend on the browser's zone; sending day
  keys and resolving them next to the data keeps every count on the page consistent.
- A hover preview must not rebuild the grid (the element under the pointer would be replaced and
  `mouseenter` fires again); toggling classes keeps it stable.

**Ideas for later**

1. Date range on the spend page reusing the same picker (today it has 7d / 30d / all).
2. "Export these runs" (CSV) for the current filter and range.
3. Per-day mini histogram above the calendar (runs per day, failures red), like Grafana's range picker.

## 2026-10-24 (12:20): spend page — click a day in the chart to see only that day

Category: **interaction / accessibility** (spend page, not touched since 10-22). The last rounds
were the runs / task lists, SQLite and 2FA; this makes the daily chart a way in, not just a picture.

**Done**

- Every bar of the daily spend chart is now a button. Click (or Enter / Space) picks that day:
  the total, the breakdown table (any tab: task, provider / model, model, provider) and the CSV
  export narrow to it; click again, the chip's × or Esc goes back to the whole range. The picked
  bar stays blue and its date label bold, the others fade to half; the bars switch at once and the
  table follows when its answer is in (cached per range + tab + day, so going back is instant, no
  skeleton, no flash).
- Next to the total a small chip says "仅 10月21日周三 / Only Wed, Oct 21", and a "当天运行 / Runs
  that day →" link opens `#/runs?from=…&to=…` (the date filter from the 09:20 round).
- Keyboard: the chart is one tab stop (roving tabindex on the picked day, else today); ←/→ move
  day by day, Home / End jump to the ends, the focused bar shows its tooltip (date · amount · what
  a click does) and a focus ring. Screen readers get `aria-pressed` and the date + amount as label.
- The view is in the address: `#/spend?range=30d&by=model&day=2026-10-21` (defaults left out),
  changed with `history.replaceState`, so reload, back / forward and links reopen it; plain
  `#/spend` keeps the last range and tab but not the day. Switching to a range that no longer
  holds the day drops the day.
- API: `GET /api/spend/breakdown` and `/api/spend/export` take `day=YYYY-MM-DD` (a real date not
  before the range start, else 400); the per-day series still covers the whole range so the chart
  doesn't move. Day goes in as a parameter. The export file is named `tidebot-spend_<day>.csv`.
- Same on macOS / Windows / Linux, desktop window and browser: plain buttons + CSS, Intl for the
  date, no modifier keys, transition off with reduced motion. 1024×655: chip + link on one line in
  zh-CN and en, scrollWidth 1024.
- Shots: `/workspace/tidebot-shots/spend-day-picked-1024x655.png`, `spend-day-keyboard-1024x655.png`,
  `spend-day-hover-1024x655.png` (+ `-en` for the first two). Demo data only.
- Tests: `TestSpendDay` (valid, before the range, impossible date, injection text),
  `TestSpendBreakdownDay` (rows / total narrow, series unchanged, empty day, 400s, export rows and
  file name), `TestSpendDayUI` (wiring, keys, CSS, i18n, no native dialogs).

**Inspiration**

- open-webui/open-webui `src/lib/components/admin/Analytics/ChartLine.svelte` (hovered day shows
  the per-model split, other series fade) and `Dashboard.svelte` (leaderboard rows open a
  per-model drill-down, `selectedModel` + `AnalyticsModelModal.svelte`). winfunc/opcode
  `src/components/UsageDashboard.tsx` has hover-only daily bars, which is what we had. We drill
  down in place (the table under the chart) instead of a modal, and add keyboard and the address.
  No code copied.

**Learned**

- Re-rendering the chart on pick drops focus; remembering the focused day and refocusing after the
  paint keeps arrow-key browsing smooth.
- The server must treat the day as part of the range (reject days before `since`), otherwise a
  stale link shows a total that the range chip doesn't explain.

**Ideas for later**

1. Shift-click a second bar to pick a span of days (same picker as the runs list).
2. Stack the bars by provider (colours from the provider icons) with a legend, like Open WebUI's chart.
3. Clicking a row in "by task" opens that task's runs for the picked day.

## 2026-10-24 (15:20): task form — retries, missed runs and success notices editable in the WebUI

Category: **missing feature / "everything from the WebUI"** (task form, API). The last rounds were
list filters and the spend chart; this one closes a gap: the per-task run policy (`task_policies`:
retries, catch-up, success notices) existed since phase 2 but could only be changed with
`agentctl call policy.set`.

**Done**

- The new / edit task dialog has a folded "失败与补跑 / Retries & missed runs" box under the working
  folder. Folded, its line says what is chosen ("重试 2 次 · 错过补一次", "no retry · skip missed ·
  notify on success") and updates as you change things; the breaker rule (3 failures in a row →
  paused) is in the ⓘ tooltip, not on the page.
- Inside: **失败重试** 不重试 / 最多 1–5 次 (tooltip: only temporary errors, ~1 min then ~5 min);
  **错过的运行** 跳过 / 补跑一次 / 逐个补跑（最多 5 次）; **补跑期限** 1 h / 6 h / 12 h / 1 d / 3 d / 7 d
  (an odd stored value is kept as its own option; greyed out when "skip"); **成功时也通知**.
- When the task is already failing the box opens by itself with a warning line
  "已连续失败 2 次，再失败 1 次会自动暂停。".
- API: `GET /api/tasks/{id}/form` returns `form.policy`, `fail_streak`, `breaker_at`;
  `POST /api/tasks` and `PUT /api/tasks/{id}` take an optional `policy`
  (`retry_max` 0–5, `catchup` skip|run_once|run_all, `catchup_grace_minutes` 1–10080,
  `notify_success`). Left out = unchanged (older pages keep working). Priority, allowed tools,
  side effects and the failure count are never touched by the form. Values are validated before
  anything is written; SQL is the existing parameterized upsert.
- Priority ("urgent may run on battery") is deliberately **not** shown: nothing reads it yet
  (`policy.AllowedOnBattery` has no caller), and a switch that does nothing is worse than none.
- Same on macOS / Windows / Linux, desktop window and browser: native `<select>` / checkbox /
  `<details>`, no paths or modifier keys. 1024×655: the box fits the dialog, footer buttons stay
  visible, page scrollWidth 1024 in zh-CN and en.
- Shots: `/workspace/shots/task-policy-open-1024x655.png`, `task-policy-changed-1024x655.png`,
  `task-policy-new-collapsed-1024x655.png` (+ `-en` for the first two). Demo data only.
- Tests: `TestTaskFormPolicy` (defaults shown, create with policy, edit keeps priority / tools /
  failure count, leaving `policy` out changes nothing, six bad bodies incl. injection text → 400 and
  nothing written), `TestTaskFormPolicyUI` (wiring, JS defaults equal `policy.Default`, CSS, i18n,
  no native dialogs).

**Inspiration**

- louislam/uptime-kuma `src/pages/EditMonitor.vue`: "Retries" sits next to the interval with a
  one-line description, the retry interval only appears when retries > 0, and "Resend notification
  if down X times" says in its label what the number means ("resend every 3 times" / "disabled").
  We fold the block (most people keep defaults), put the meaning on the folded line, disable the
  window when it doesn't apply, and keep long explanations in tooltips. No code copied.

**Learned**

- An optional pointer field (`policy` omitted vs. present) lets a form grow without breaking an
  older open tab: its PUT just leaves the policy alone.
- `policy.Store.Put` already preserves `consecutive_failures`; reading the row first and changing
  only the four form fields is what keeps the CLI-only fields (priority, tools) safe.

**Ideas for later**

1. Wire `priority` for real (skip non-urgent scheduled runs on battery / low-power mode on all three
   OSes), then show it in this box.
2. Make the breaker threshold per task (3 now, fixed) and show "paused by breaker" with a
   one-click "reset and resume" on the task page.
3. Show the policy summary as a tooltip on the task detail header, so you don't need to open Edit.

## 2026-10-24 (18:20): paused tasks say why — and resuming starts the failure count over

Category: **logic / correctness** (pause reasons, circuit breaker). The last rounds were list
filters, the spend chart and the task form; this one fixes a quiet bug and a blind spot.

**Done**

- Bug: after the breaker paused a task (3 failures in a row) and you resumed it, the failure count
  stayed at 3, so the very next failure paused it again. Resuming a paused task (WebUI, agentctl,
  saving the form as enabled) now clears `consecutive_failures`; saving an already active task does
  not touch it.
- Every pause now records why and when: new columns `tasks.pause_kind` (user / breaker / budget /
  pack / plugin), `pause_detail` (error class, budget reason, plugin id; ≤300 bytes, cut on a rune
  boundary) and `paused_at` (migration 011). Pausing an already paused task keeps the first reason
  (a breaker trip after a manual pause does not relabel it, nor does editing a paused task).
  Leaving paused / archiving clears them. Tasks paused before this have an empty kind and just say
  "paused". The reason strings that `SetStatus` and `Cost.PauseTask` used to accept and drop are
  now stored; the breaker uses `Tasks.Pause(…, breaker, class)` instead of the budget pause.
- WebUI: the status pill says it in a word — 失败暂停 / Paused · failing, 预算暂停 / Paused · budget,
  待启用 / To review, 插件暂停 / Paused · plugin, plain 已暂停 for a manual pause — and the tooltip
  (hover or keyboard focus) gives the sentence: when, and the localized error class or budget
  reason. Same on the task list, task page and home heartbeat rows (`/api/tasks`,
  `/api/tasks/{id}`, `/api/dashboard` carry `pause_kind`, `pause_detail`, `paused_at`,
  `breaker_at`).
- Task page, automatic pauses only: one full-width line under the header, e.g.
  "连续失败 3 次（认证失败），已自动暂停。修好后点「恢复」，失败计数会清零。" with a link to the
  task's failed runs (breaker) or the spend page (budget). Resume shows a toast saying the count
  was reset (or, for budget, that it may pause again). No native dialogs.
- Same on macOS / Windows / Linux, desktop window and browser: plain SQL + CSS tooltips, no paths,
  no modifier keys. 1024×655: the line fits on one row in zh-CN and en, page scrollWidth 1024.
- Shots: `/workspace/tidebot-shots/task-paused-breaker-1024x655.png`,
  `task-list-pause-reason-1024x655.png` (+ `-en`). Demo data only.
- Tests: `TestPauseReason` (tasksvc: kinds, first reason kept, resume resets the count,
  active→active doesn't, rune-safe cut, injection text stored verbatim, archive clears),
  `TestRetryThenBreaker` extended (breaker records the class; after resume one failure doesn't
  pause again), `TestPauseReasonAPI` (detail / list / dashboard, budget pause, resume via CSRF,
  manual pause = user, editing keeps the reason), `TestPauseReasonUI` (wiring, escaping, CSS,
  i18n incl. every error class, no native dialogs).

**Inspiration**

- n8n-io/n8n `packages/cli/src/active-workflow-manager.ts` + `activation-errors.service`: when a
  trigger fails, the workflow is deactivated and the error is registered per workflow, then cleared
  on successful re-activation; `packages/frontend/editor-ui/src/features/workflows/components/
  WorkflowActivationErrorMessage.vue` shows it on demand. louislam/uptime-kuma
  `src/pages/Details.vue` has the paused-monitor Resume button in the header. We keep the reason
  on the task row, show it in a word + tooltip, and clear the failure count on resume. No code
  copied.

**Learned**

- Status writes came from three places with a `reason` argument that was silently dropped
  (`_ = reason`); a single transition function (old status → new) is the right place for "only
  on change" side effects like the reset.
- SQLite `UPDATE … SET` reads old values, but a read-then-write in one transaction is clearer when
  the side effect touches another table.

**Ideas for later**

1. Breaker notice: include a "Resume" button in the desktop notification / notification center.
2. "Paused · failing" filter chip on the task list (subset of paused).
3. Auto-resume budget pauses at the budget reset (opt-in), since the reason is now known.

## 2026-10-24 (21:20): notification channels set up in the WebUI — add, test, switch off, remove

Category: **missing feature / "everything from the WebUI"** (Settings → Notifications, notify
core). The last rounds were pause reasons, the task form and list filters; this one closes a gap
the settings page itself pointed at: "添加渠道：`agentctl notify add`" — a command that didn't
even exist (only the raw `notify.add_channel` IPC call did).

**Done**

- Settings → Notifications → 通知渠道 has an "添加渠道" button. The dialog starts with a 3×2 card
  picker (飞书 / 钉钉 / 企业微信 / Telegram / 邮件 / Webhook; native radios, so arrow keys work)
  and shows only that kind's fields, with a one-line "where to get it" hint per kind.
- Auto-fill: pasting a robot URL picks the kind from its host (open.feishu.cn / larksuite,
  oapi.dingtalk.com, qyapi.weixin.qq.com) and says "已识别为钉钉机器人". The name follows the kind
  ("钉钉", then "钉钉 2" if taken) until you type your own. For email, the sender address fills
  SMTP server, port, user name and recipient (same lookup as the email plugin, typed fields are
  never overwritten) and says when the provider wants an app password / 授权码.
- "发送测试" sends what is in the form **before saving** (and, when editing, with the saved URL /
  secret if those fields are left blank). The result shows inline under the form, not as a toast.
  Each saved channel also has a test icon, edit, delete (in-app confirm, says per-task rules go
  too) and an on/off switch; the card repaints in place, no page reload.
- "发送哪些通知": six groups instead of raw event names — failed or didn't finish, needs my
  confirmation, budget alerts, paused after failures, run succeeded (only tasks with success
  notices on), system (update, rollback, disk low, plugin messages); each has a tooltip. Default:
  all but "run succeeded". Stored as one all-task rule per event, so routing, quiet hours and the
  digest work as before; per-task rules are kept on edit. A channel made with a `*` rule shows
  "全部通知" and says saving narrows it to the ticked groups.
- Secrets: robot URLs carry the access token, so URLs saved from the WebUI now go to the secret
  store (`notify.<id>.url`) instead of `config_json`, like the bot token / SMTP password / signing
  secret. The page only gets the host back (tooltip); edit fields say "已保存，留空则不修改".
  Removing a channel deletes both secrets. Unknown config keys are dropped server-side.
- Fixes found on the way:
  - DingTalk and Feishu answer HTTP 200 when they refuse a message (keyword missing, bad
    signature, bad token). That counted as delivered. Robot replies are now checked
    (`errcode` / `code` / `StatusCode` / Telegram `ok`), so tests and real notices report e.g.
    "code 19001: … access token invalid".
  - Email on port 465 (what QQ / 163 / Gmail list) never worked: `smtp.SendMail` only speaks
    STARTTLS. 465 now uses implicit TLS (TLS 1.2+); "Name <me@x>" senders and `;`-separated
    recipients work.
  - DingTalk "加签" and Feishu "签名校验" bots are supported (optional signing secret).
- Errors come back as stable codes (`notify: url_bad`, …) and are shown in the UI language.
- Same on macOS / Windows / Linux, desktop window and browser: no paths, no modifier keys, no
  `:has()` (selected card is a class, for older WebKitGTK). 1024×655: the dialog scrolls inside,
  header and "发送测试 / 取消 / 保存" stay visible; zh-CN and en checked.
- Shots (demo data only): `/workspace/tidebot-shots/settings-notify-channels-zh-CN-1024x655.png`,
  `settings-notify-add-zh-CN-1024x655.png`, `settings-notify-del-zh-CN-1024x655.png` (first two also `-en`).
- Tests: `TestChannelSaveSummaryTest` (validation codes, URL and secrets kept out of the
  database, groups → rules, per-task rules survive edits, blank fields keep stored values, test
  before saving, robot refusals, DingTalk / Feishu signing, enable switch, legacy `*` channels,
  remove clears secrets), `TestSplitAddrs`, `TestNotifyChannelsFromWebUI` (through `CallExt`:
  overview never carries the token, a failed run reaches the channel, off stops it),
  `TestNotifyChannelsUI` (wiring, in-app confirm, i18n for every code / kind / group).

**Inspiration**

- louislam/uptime-kuma `src/components/NotificationDialog.vue`: one dialog for every provider,
  the default name follows the type until edited (`getUniqueDefaultName` adds a number when the
  name is taken), and the Test button sends the unsaved form (`server/server.js`
  `testNotification`). `server/notification-providers/dingding.js` signs with
  `timestamp + "\n" + secret` and treats any `errmsg` other than "ok" as a failure — that's what
  showed our 200-but-refused gap. Ideas only, no code copied.

**Learned**

- Chat robots (DingTalk, WeCom, Feishu) report most failures in the JSON body with HTTP 200; a
  status-code check alone says "sent" for a message nobody saw.
- Go's `net/smtp.SendMail` has no implicit-TLS mode; port 465 needs `tls.Dial` + `smtp.NewClient`.

**Ideas for later**

1. Per-task channel choice in the task form ("send this task's failures to 运维群"), using the
   per-task rules that already exist.
2. Show the last delivery result per channel (time, ok / error) from the `notifications` table,
   in the row tooltip.
3. Reuse a configured WeCom / DingTalk / email plugin as a channel with one click instead of
   pasting the URL twice.

## 2026-10-25 (00:20): skill page — browse a version's files, not only their diff

Category: **skills / missing feature** (skill detail page). The last rounds were all tasks,
runs, notifications and settings; this one goes back to the skill library. Until now the only
way to read what a skill actually contains was to download the .zip, or to compare two
versions and read the diff — a skill with one version showed nothing at all. The
`/api/skills/<id>/files` list existed but nothing used it (idea 4 of the 10-22 20:30 entry).

**Done**

- The skill page's main card has two tabs: 版本对比 (the diff, as before) and 文件. 文件 shows
  the chosen version's files as a tree (folders first, collapsible, sizes in the tooltip) and
  the selected file read-only with line numbers, in a box that scrolls inside itself (fits
  1024×655; the tree and the file scroll separately).
- Auto-pick: opens SKILL.md (or the shallowest `*/SKILL.md`, else the first file); switching
  versions keeps the same file when it exists there. Each row in 版本树 has a folder icon that
  opens that version's files directly.
- File header: path, size (lines and line-ending kind — LF / CRLF (Windows) / mixed — in the
  tooltip), copy button. Binary files and files over 256 KB show a short note and the version
  download instead of garbage; empty files say so.
- The address keeps the tab, version and file (`?tab=files&v=…&f=…`), so reload, back/forward
  and a pasted link open the same view; rollback / promote reloads stay on the files tab.
- Keyboard: ←/→ switch tabs; in the tree ↑/↓/Home/End move, → opens a folder, ← closes it or
  jumps to its folder, Enter opens a file.
- No flicker: switching tabs only toggles panes; opening a file keeps the previous one on screen
  (dimmed) until the new one arrives; answers for a file that is no longer selected are
  dropped; recently opened files (≤ 24) are kept in memory since versions never change.
- New endpoint `GET /api/skills/<id>/file?v=<version>&path=<file>`: the path must be a key of
  that version's own file list (no `..`, absolute or made-up paths — 404), the version must
  belong to the skill (400), GET only. Text comes back as stored (CRLF kept) and is escaped by
  the page; no HTTP caching, so nothing lingers in the browser cache after sign-out.
- Same on macOS / Windows / Linux, desktop window and browser: paths are the stored `/` form,
  monospace stack already covers Menlo / Consolas / Liberation Mono, no modifier keys involved.
- Shots (demo data only): `/workspace/tidebot-shots/skill-files-zh-CN-1024x655.png`,
  `skill-files-binary-zh-CN-1024x655.png`, `skill-files-en-1024x655.png`.
- Tests: `TestSkillFileView` (CRLF / LF text, line counts, binary, oversized, traversal and
  unknown paths, foreign version / skill, method), `TestViewSkillFileEOL`,
  `TestSkillFileBrowserUI` (tabs, endpoint wiring, escaping, address state, i18n).

**Inspiration**

- siteboon/claudecodeui `src/modules/code-editor/hooks/useCodeEditorDocument.ts`: a load
  counter (`loadGenerationRef`) so a slow read for a file you already left never lands on the
  one you opened next, and CRLF normalised for display; `CodeEditorBinaryFile.tsx`: binary files
  get a placeholder instead of being decoded; `src/modules/file-tree/hooks/useExpandedDirectories.ts`
  for per-folder open state. Ideas only, no code copied.

**Learned**

- The import keeps files byte for byte, so a skill made on Windows really carries CRLF; showing
  that in the tooltip explains "why does the diff mark every line" without cluttering the view.
- A gutter `<pre>` next to a `white-space: pre` text `<pre>` keeps line numbers aligned with no
  per-line DOM, which is plenty for files up to the 256 KB limit.

**Ideas for later**

1. Download a single file from the viewer (attachment, `nosniff`), handy for scripts.
2. Search inside the open file (Ctrl/⌘+F inside the box, n / N to jump) and a "find in all files".
3. Render SKILL.md as formatted Markdown with a "源码 / 预览" toggle.
4. Mark files changed since the previous version in the tree (dot), and jump from a tree file to
   its diff.

## 2026-10-25 (03:20): runs don't start on an almost full disk — skipped with the reason

Category: **logic / correctness + resource usage** (runner, Settings › Data › Storage). The last
rounds were features and list UX (skill files, notification channels, pause reasons, task form);
this one picks up idea 2 of the 10-21 16:00 low-disk entry. The low-disk warning only said
"clean up soon"; a run started with a few MB left still launched Claude Code and then failed
half-way (cut-off log, SQLite write errors, partial artifacts), was retried, and after three of
those the breaker paused the task — which stayed paused after the disk was cleaned.

**Done**

- New runner hook `Options.StartCheck`, run for **every** run once it has its slot (queued or
  not), before `BeforeStart`. A "no" ends the run as skipped through the existing
  `finishSkipped` path: run row `skipped`, no start time, no cost, log line, slot passed on.
- `App.diskStartCheck`: measures the data volume fresh (one `statfs` / `GetDiskFreeSpaceEx`, not
  the 15-minute cache) and refuses under the minimum, with the message
  `skipped: disk full: <free bytes>/<minimum bytes>`. It refreshes the cached disk status (tray,
  storage card) and sends the `disk_low` notice if due (the watcher's daily throttle applies).
  A disk that can't be measured never blocks (same as before on unsupported platforms).
- Skipped for a full disk = not the task's fault: no retry, no breaker strike, no "run failed"
  notice. Once space is back, the next run starts normally — nothing to resume by hand.
- Settings › Data › Storage has a new row 最少保留空间 / Keep free at least: 不检查, 100 MB,
  200 MB, 500 MB (default), 1 GB. Saved through `storage.settings` (`min_free_mb`, 0..1024,
  validated server-side; other values rejected, a corrupt stored value falls back to 500).
  The ceiling is the low-disk warning's lowest threshold (1 GiB), so a blocked disk is always
  also "low" and the tray / bell already point at it.
- While blocked, the storage card shows a red notice ("磁盘只剩 312 MB，低于最少保留空间
  500 MB，新运行暂不启动……") instead of the yellow low-disk one; the rules (checked at each
  start, not a failure, no retry, no pause) are in its ⓘ tooltip.
- A run skipped this way explains itself in the viewer's language on the status pill tooltip and
  the run page ("未启动：数据所在磁盘只剩 X，低于最少保留空间 Y。在「设置 › 数据 › 存储空间」
  清理……").
- Same on macOS / Windows / Linux, desktop window and browser: the measurement is the existing
  cross-platform `internal/diskspace`; the UI is a plain `<select>` and text, no paths or
  modifier keys. 1024×655 checked in zh-CN and en (the row fits on one line in both).
- Shots (demo data, disk status faked in the browser): `/workspace/tidebot-shots/storage-min-free-zh-CN-1024x655.png`,
  `storage-min-free-en-1024x655.png`.
- Tests: `TestStartCheckSkipsUnqueuedRun` (runner: skipped row, BeforeStart not called, slot
  freed), `TestDiskFloorSkipsRuns` (4 scheduled runs skipped with the figures, no retry, no
  breaker, no failed event, one disk_low notice, cache refreshed; runs again once space is
  freed; 0 = off; unknown disk never blocks), `TestDiskFloorSetting` (default, 1 GB → blocked,
  bad values rejected, retention untouched, corrupt value falls back), `TestDiskFloorUI`
  (wiring, the page's pattern matches the stored message, i18n in both languages).

**Inspiration**

- Sonarr/Sonarr `src/NzbDrone.Core/MediaFiles/EpisodeImport/Specifications/FreeSpaceSpecification.cs`:
  a "minimum free space" setting checked right before each import, rejecting with a specific
  reason ("Not enough free space"), skippable by a setting, and accepting when the free space
  can't be determined (`freeSpace` has no value → accept). tidebot borrows the check-at-start,
  the explicit reason and the fail-open rule. Ideas only, no code copied.

**Learned**

- The runner already had a clean "turned away at the slot" path for budgets; a second hook
  (`StartCheck`, every run) was cleaner than making `BeforeStart` run twice for unqueued runs,
  which would have logged and notified budget breaches twice.
- Budget-skipped queued runs still count as a breaker failure in `runWithPolicy`; disk-full
  skips must not, or a cleaned-up disk leaves tasks paused.

**Ideas for later**

1. When a run is refused for a full disk, run the retention cleanup once (throttled, e.g.
   once per 30 min) and re-check before giving up; say "freed X" in the notice.
2. Group "skipped: disk full" runs in the bell ("3 runs skipped, disk almost full") instead of
   relying on the daily low-disk notice.
3. Desktop tray: show "运行已暂停启动（磁盘已满）" as its own problem line while blocked.
4. Also check the task's working folder volume when it lives on another disk.

## 2026-10-25 (06:20): j / k through list rows — open, tick, run without the mouse

Category: **UI interaction / keyboard** (tasks, runs, skills, home and task-detail lists). The
last rounds were features and backend (disk floor, skill files, channels, pause reasons); this
one picks up the keyboard line of `docs/ux-iteration-plan.md` §3 ("列表内 J/K 上下、Enter 打开"),
which was never done: the shortcut sheet had `↑ ↓ 在列表里上下移动`, but that only worked in the
command palette — on a list page you still had to reach for the mouse for every row.

**Done**

- On any page with clickable rows (`[data-href]`: task list, runs list, skills list, home
  "running now", task-detail recent runs), **j / k** move a highlighted row (tinted
  `--accent-50` plus a 3 px accent bar on the left, no layout shift), clamped at both ends,
  hidden rows skipped, scrolled into view only when needed, focus moved to the row
  (`aria-current`, `tabindex=-1` added and removed again).
- **Enter / o** open the row, **x** ticks its checkbox (task list → the batch bar shows up),
  **r** runs it now (task list), **Esc** drops the highlight, **/** jumps to the page's search
  box (task and run lists show a small `/` badge in the box, hidden while focused or filled).
- The row is remembered per page by its link, not its node: live repaints (runs list polls
  every 2 s, search, sort, chip counts) put the highlight back once per frame through one
  `MutationObserver`; coming back from a row (Back, or a click on the nav) shows the row you
  opened. A click on a row is remembered the same way.
- Enter / o / x / r only act on a row picked with j / k on this visit; a row remembered from a
  click is only highlighted, so `o` on a task page still opens its folder and a focused button
  or chip keeps its own Enter.
- Skills list rows are now clickable like tasks and runs (whole row opens the skill).
- Shortcut sheet: new lines for j / k, Enter / o, x / r and /; `↑ ↓` now says it is for the
  palette and menus. Short enough to fit one or two lines at 1024×655 in both languages.
- Same on macOS / Windows / Linux, desktop window and browser: plain letter keys, no ⌘ / Ctrl,
  ignored while typing, with a modifier held, or with a dialog / the palette open; no native
  dialogs.
- Shot (demo data): `/workspace/tidebot-shots/list-keyboard-cursor-zh-CN-1024x655.png`.
- Tests: `TestListKeyboardCursor` (wiring, CSS, i18n; under node with a small fake DOM:
  clamping, hidden rows, focus / scroll, x / r clicks, repaint keeps the row, Enter opens,
  remembered-but-not-picked row ignores o, focused button keeps Enter, Esc, /, click memory).

**Inspiration**

- miniflux/v2 `internal/ui/static/js/app.js`: `goToListItem(offset)` moves a `.current-item`
  class over the visible `.items .item`, focuses it and scrolls only when it is off screen;
  `keyboard_handler.js` + the `keyboardHandler.on("j" / "k" / "o" / "Enter" / "/")` table, with
  keys ignored in inputs. tidebot borrows the class-on-the-row model, visible-only rows, focus
  and "scroll only when needed", and keys the row by its link so repaints don't lose it. Ideas
  only, no code copied.

**Learned**

- A `tr` with `tabindex` picks up the global `[tabindex]:focus-visible` ring, which draws
  oddly on table rows; the cursor style turns it off and uses the left bar instead.
- A search input with no `min-width: 0` keeps its intrinsic width in a flex row and pushes a
  trailing badge out of a narrow box (runs toolbar at 1024 px in English).

**Ideas for later**

1. Shift+j / Shift+k to extend a selection range (tick several tasks), and `x` on the runs list
   once it has bulk actions.
2. Same cursor in the plugins and notification-channel lists (cards, not rows, today).
3. `g g` / `G` for first / last row (Miniflux has them), and j / k on the run-detail log lines.
4. Show the `/` badge on the skills list once it gets a search box.

## 2026-10-25 (09:20): runs going at the same time share what is left of a budget

Category: **logic / correctness (cost control)**. The last rounds were UI and keyboard (j / k,
skill files, channels) and the disk floor; this one picks up idea 1 of the 10-23 06:20 entry
("budget reservations"). Every run's cap was "what is left of today's budget", read from the
ledger when it was admitted. Two runs admitted together (cron at the same minute, a manual run
next to a scheduled one, max concurrency 2+) each got the **whole** remainder: with $0.30 left,
two runs could spend $0.60 between them, and the daily cap was overshot by one run per slot.

**Done**

- New `cost.ReserveRun` (+ `Hold`, `Held`, in `internal/cost/hold.go`): the last budget step
  before a run launches. Under one lock it reads every daily money budget the run counts
  against (global, its bucket, its task), subtracts what the runs already going **still hold**
  (their cap minus what they have spent so far — the live ledger already counts that part, so
  nothing is counted twice), lowers the run's cap to the room left and holds it until the run
  ends. Holds are in memory only (they die with the process, like the runs).
- Less than a cent left once the running runs are counted → the run is **skipped**, not failed:
  run row `skipped` with `skipped: budget blocked: global/ daily USD cap held by 2 running
  (3.1248+1.8600/5.0000)`, a `held` budget event with action "skip", no pause, no retry, no
  breaker strike, no "budget breached" notice. The next run after they end starts normally.
- A cap already reached at that moment is the normal breach (budget's own action), shared with
  the request-time check through `App.budgetBreach`.
- What a run spends moves from its hold to the ledger as it goes (`OnUsage` → `SetSpent`); the
  hold is released after the final ledger write, also for stopped, failed, skipped and quit runs.
  A hold from before midnight doesn't count against the new day.
- Subscription runs (no money cap) hold nothing; Anthropic's `--max-budget-usd` gets the lowered
  cap too.
- New runner hook `Options.AtStart`, run for **every** run last (after `StartCheck` and
  `BeforeStart`), so unqueued runs also get a visible skipped row instead of vanishing.
- Spend page: the budget row shows the held part as a light striped segment after the spent
  part and "+$1.35" next to the amount; the tooltip says how many runs hold how much. The
  "remaining" card is cap − spent − held, explained in its tooltip only when something is held.
- Run page / pill tooltip explain the held skip in the viewer's language (different text from a
  real budget stop: "任务不会被暂停"); spend events and the notification center translate the
  held reason; global events no longer start with an empty name ("：已达…" → "全局：已达…").
- Same on macOS / Windows / Linux, desktop and browser: pure Go + CSS gradient, no paths or keys.
- Shot (demo data, held figures faked in the browser since the demo provider is a subscription):
  `/workspace/tidebot-shots/budget-held-zh-CN-1024x655.png`.
- Tests: `TestReserveRunSharesRemainder` (second run gets only the rest, third held, spend moves
  from hold to ledger once, release gives it back, Summary shows held), `TestReserveRunScopesAndCurrency`
  (task / bucket only count their own holds, CNY caps, no cap holds nothing),
  `TestReserveRunBreachAndNewDay`, `TestReserveRunConcurrent` (20 goroutines never hold more
  than the cap), `TestAtStartRunsForEveryRun` (runner order and skip), `TestConcurrentRunsShareBudget`
  (app, metered provider: second run skipped with the reason, task active, no strike, no retry,
  no breach notice, runs again after the first one stops), `TestSubscriptionRunHoldsNothing`,
  `TestBudgetHeldUI` (Go and JS patterns match the core's text, i18n in both languages).

**Inspiration**

- BerriAI/litellm `litellm/proxy/hooks/parallel_request_limiter_v3.py`: `reserve_tpm_tokens`
  reserves the estimated tokens against every limited scope **before** the upstream call ("so
  concurrent requests cannot all observe 'under limit' before any of them increments the
  counter"), all-or-nothing; post-call reconciliation applies only the actual − reserved delta;
  `_refund_reserved_tokens` gives back a reservation; a `reservation_released` flag makes the
  first release win. tidebot borrows check-and-reserve as one step, reserve per scope,
  reconcile against actual spend and idempotent release. Ideas only, no code copied.

**Learned**

- The live ledger (10-22 12:00) made this cheap: a hold only needs to carry the *unspent* part,
  so a long run's hold shrinks on its own and the totals are never counted twice.
- `.progress` has `overflow: hidden`, so a CSS tooltip on the bar itself is clipped; the tooltip
  sits on the amount instead.

**Ideas for later**

1. Instead of skipping a held-off scheduled run, wait (bounded) for a running run to release
   its hold and start then.
2. Show "预留 $x" in the Home "running now" row and the run page's live cost while it runs.
3. Hold quota-based subscription runs against the provider's request / token window the same way.
4. Fix the budget-row selects wrapping under their labels at 1024 px (seen in the shot).

## 2026-10-25 (12:00): spend page budgets — one aligned row each, the cap edits in place

Category: **UI polish / interaction** (spend page, settings › budgets). The last rounds were
mostly features and backend (budget holds, j / k, disk floor, skill files, channels). This one
picks up idea 4 of 09:20: at 1024 px the budget card put "超出后" and "提醒阈值" on lines of
their own with the selects wrapping under them, so four budgets took the whole window. While
fixing it, a real bug turned up: saving a cap in Settings › Budgets sent no action / warn level,
and the server filled in the defaults, so **changing the global cap silently reset "on breach"
to "pause the task" and "warn at" to 80 %** (and a bucket cap wiped the bucket's token cap).

**Done**

- Budgets card is full width with one 48 px row per scope on a shared grid: scope · period, bar,
  used / cap (right-aligned, tabular numbers), "on breach" select, "warn at" select. Column heads
  replace the label next to every select; the breach explanation moved to the ⓘ tip; a "预算设置"
  link goes to Settings › Budgets. 5 budgets now take ~340 px (card included) instead of ~800 px. Task names that
  don't fit are cut with "…" and shown in full on hover.
- Budget events sit full width below, cells top-aligned, so the detail column no longer wraps
  into five lines.
- The cap is a button that reads as text: hover / focus draws a box around it without moving
  anything, tooltip "点击修改上限，填 0 取消上限". Click or Enter opens a small input in place
  (currency sign in front, grows with the text). Enter or leaving the field saves, Esc cancels,
  empty or unchanged puts the old value back. Accepts `5`, `2.50`, `12,5`, `$7.25`, `¥ 30`; a bad
  value turns the box red with a message and stays open (leaving it then just drops it). `0`
  asks first in the app's own dialog ("不再限制「辅助」的花费？" / "不设上限"). After saving, the page
  refreshes in place (scroll kept, no skeleton), focus returns to the cap, toast says the new cap.
- `/api/budgets` is now a partial update: any of `max_usd`, `max_tokens`, `action`, `warn_at_pct`
  left out keeps its current value (new budgets still get pause / 80 %). Also validates
  `max_usd` 0..1 000 000 (rounded to 4 decimals), `max_tokens` 0..10¹², period `day` / `run`.
  The spend page and Settings send only the field that changed. Parameterized SQL as before.
- English action labels shortened ("Block, pause task" / "Block run only") so they fit the 160 px
  select; zh-CN unchanged.
- Under 800 px each row folds into three lines (name + amount, bar, the two selects).
- Same on macOS / Windows / Linux, desktop window and browser: plain CSS grid (`display:
  contents` rows, no `:has()` for older WebKitGTK), the outline uses `::before` so the
  `[data-tip]::after` tooltip still works, no modifier keys, no native dialogs. 1024×655 checked
  in zh-CN and en: no cell overflows, page scrollWidth 1024.
- Shots (demo data, spend figures faked in the browser since the demo provider is a
  subscription): `/workspace/tidebot-shots/budget-rows-cap-edit-zh-CN-1024x655.png`,
  `budget-rows-cap-editing-zh-CN-1024x655.png`, `budget-rows-cap-edit-en-1024x655.png`.
- Tests: `TestBudgetPartialUpdate` (cap change keeps action / warn / token cap, token cap off,
  warn change keeps caps, 0 turns the cap off, new bucket gets defaults and 4-decimal rounding,
  7 bad requests rejected without touching the budget), `TestBudgetRowsUI` (markup, in-app
  confirm, only the changed field posted from both pages, CSS, i18n in both languages, label
  length; `parseCap` under node: 14 inputs).

**Inspiration**

- n8n-io/n8n `packages/frontend/@n8n/design-system/src/components/N8nInlineTextEdit/InlineTextEdit.vue`:
  the preview text gets a box drawn by a pseudo-element on hover / focus so nothing shifts, the
  input width follows a hidden measuring span between a min and max, `submit-mode="both"`
  (Enter and blur submit), cancel restores the model value, an empty value is not submitted.
  tidebot borrows the no-shift outline, grow-with-text width (via `size`), Enter / blur / Esc
  and "empty reverts". Ideas only, no code copied.

**Learned**

- A form POST that sends "all fields" from a page that only shows some of them is how the
  settings page reset the breach action: partial updates on the server are the safer default.
- The shared tooltip is `[data-tip]::after`; any decoration on an element with `data-tip` has
  to use `::before`, or the tooltip text renders on top of it.
- Grid rows with `display: contents` can't take `order` per row on small screens; folding them
  means switching the row back to its own grid.

**Ideas for later**

1. Add a budget for any task from the spend page (a "+" row with a task picker) instead of
   only from the task form.
2. Edit the global token cap the same way (it is only in Settings today).
3. Undo in the toast after removing a cap (8 s, puts the old cap back).
4. Same inline edit for the per-run cap and timeout on the task page.

## 2026-10-25 (15:20): notification channels show how their last send went

Category: **correctness / observability** (Settings › Notifications, notify core). The last
rounds were budget holds (logic) and the budget rows (UI). Idea 2 of 10-24 21:20: once a channel
was saved, nothing in the WebUI said whether notices still arrived. A DingTalk robot that
started refusing (keyword check, IP allow-list, rotated token) failed silently until someone
noticed missing alerts.

**Done**

- Each channel row has a status cell between the name and the buttons: green dot + "已送达 · 2
  小时前", red "发送失败 · 1 分钟前" / "连续失败 3 次 · …", hollow "未发送过". The relative time
  ticks with the shared `[data-ago]` ticker. Details only in the tooltip (anchored right, also on
  keyboard focus): local time, time taken, the cleaned error, the last success or "还没有成功送达过".
- Core: migration `012_notify_delivery.sql` adds `last_at / last_ok / last_error / last_ms /
  last_ok_at / fail_streak` to `notify_channels`. `Service.Send` (dispatch, digest, legacy
  `notify.test`) records every send; `Test` records only when the form equals the saved setup
  (the list's test button), so trying unsaved values never colours the row. Saving a new
  address / secret / config forgets the old result; rename or regroup keeps it.
- Errors are stored cleaned: URLs reduced to their host (robot URLs carry access tokens,
  `user:pass@` dropped), Telegram-style bot tokens masked, whitespace folded, 200 runes max.
- The list's test button refreshes the row in place (no remount flash) and keeps focus.
- Same on macOS / Windows / Linux, desktop window and browser: plain CSS, no paths, no
  modifier keys, no native dialogs; times in the WebUI's time zone. zh-CN and en for every string;
  1024×655 checked in both languages (scrollWidth 1024).
- Shot (demo data, local fake robot): `/workspace/tidebot-shots/2026-10-25-notify-last-delivery.png`.
- Tests: `TestChannelLastDelivery` (ok, two refusals → streak 2 with the last success kept,
  unsaved form test ignored, saved test clears the streak, rename keeps / new URL resets, no
  token stored), `TestCleanErr` (Telegram path token, DingTalk query token, `user:pass@`, bare bot
  token, whitespace, length cut), `TestNotifyLastDeliveryUI` (markup, tooltip, in-place refresh,
  CSS, i18n in both languages, label length).

**Inspiration**

- healthchecks/healthchecks `hc/api/models.py` (`Channel.notify`: every send, test sends too,
  updates `last_notify`, `last_notify_duration` and `last_error` on the channel row) and
  `templates/front/channels.html` ("Failed, 2 hours ago" in red with the error in a tooltip,
  "Delivered, …", "Never"). tidebot borrows the per-channel last result, the duration and the
  short-state-plus-tooltip layout, and adds the failure streak and last success. Ideas only, no
  code copied.

**Learned**

- `postJSON` already never echoed the URL, but SMTP / `net` errors can; cleaning at the one
  place that stores errors is cheaper than auditing every transport.
- A test of edited form values must not change the saved row's state, or "test before save"
  would paint a broken channel green.

**Ideas for later**

1. After N failures in a row (e.g. 5), raise one in-app notice "「钉钉 · 运维群」连续 5 次发送失败"
   (bell + desktop), never through the failing channel itself.
2. Show a red dot on Settings › Notifications in the settings nav while a channel is failing.
3. A small per-channel history (last 10 sends) in the edit dialog, from the `notifications` table.
4. Healthchecks disables a channel on a permanent error (e.g. WeCom "invalid webhook key");
   tidebot could offer "停用此渠道" in the tooltip instead of doing it silently.

## 2026-10-25 (20:00): unplugged laptop — scheduled runs that can wait, wait for power

Category: **cross-platform behavior + resource usage** (core runner gate, Settings › General,
task form). The last rounds were notification status (correctness), budget rows (UI) and budget
holds (logic). final-plan §12 promised "电源感知": on battery or low battery only urgent tasks
run. `policy.AllowedOnBattery` existed but nothing called it and nothing read the battery, so a
MacBook on the train started every 30-minute Claude Code session (a CPU core busy for minutes)
just like on the desk.

**Done**

- New `internal/power`: one reading = known / has battery / on battery / percent / OS power
  saver. macOS: `/usr/bin/pmset -g batt` (full path: PATH is minimal when started from Finder),
  plus `pmset -g` for Low Power Mode (`lowpowermode 1` up to 13, `powermode 1` later) only while
  unplugged; Windows: `GetSystemPowerStatus` via `x/sys/windows` (no cgo; `SystemStatusFlag` =
  battery saver); Linux: `/sys/class/power_supply` (Mains / USB-C online, system battery
  `capacity` / `status`, device batteries like mice ignored) and `platform_profile = low-power`.
  Unknown (containers, BSD, read errors) never holds anything back. Parsers are plain functions
  tested on every OS (CRLF too).
- Rule: task priority urgent / normal / background. Settings › General › Power, mode
  「电量低时」(default): background waits whenever on battery, normal waits at ≤ 20 % or with
  the power saver on; 「用电池时」: everything but urgent waits on battery; 「不限制」: off.
  Only scheduled runs (cron + their retries) are held; Run now, buttons, webhooks, plugin
  triggers and `agentctl tasks run` always start (that RPC used the "cron" trigger; it now says
  "manual", like the WebUI's Run now).
- A held run ends as skipped with `skipped: power saving: <battery|low|saver> <pct>` (no
  retry, no breaker strike, no "failed" notice), explained in the runs list / run page in the
  viewer's language. The task is remembered; a watcher goroutine starts only then, reads the
  power once a minute and stops when nothing waits (zero polling otherwise). Power back (or a
  looser mode chosen) → each waiting task runs once, unless its missed-runs policy is "skip"
  or its catch-up window has passed — the same rules as a missed schedule.
- Readings are cached 15 s so a 09:00 burst of tasks spawns pmset once.
- Settings › General › Power card: mode select (saved on change, saved mark), the reading as
  a pill in the card head (用电池 · 18 %, warn colour when low; 接电源 / 无电池 / 无法读取), the
  rules in the row text, details in the pill tooltip; "N 个任务在等电源" notice when any wait.
- Task form › 失败与补跑: retries / missed runs / window now share one row of three, the new
  priority select ("紧急 · 用电池也照常运行" / "普通 · 电量低时等待" / "后台 · 等接上电源再跑")
  sits next to "notify on success", so the block is no taller. The folded summary adds 紧急 /
  仅接电源时运行. An older page that sends no priority keeps the task's.
- Same on macOS / Windows / Linux, desktop window and browser: plain CSS, no paths in the UI,
  no shortcuts, no native dialogs; zh-CN and en; 1024×655 checked in both (scrollWidth 1024; the
  en select fits on the row after shortening "On low battery").
- Shots (demo data, power reading faked in the browser since the box has no battery):
  `/workspace/tidebot-shots/2026-10-25-power-saving.png`, `2026-10-25-power-saving-en.png`,
  `2026-10-25-power-saving-taskform.png`.
- Tests: `TestParsePmsetBatt`, `TestParsePmsetLowPower`, `TestReadSysfs` (unplugged with a
  mouse battery, plugged, USB-C charger, server, no mains entry, missing root),
  `TestFromWindows`, `TestPowerHold` (13 mode × priority × reading cases, runs on Windows too),
  `TestPowerSavingHoldsScheduledRuns` (4 held runs without strike or failed event, manual and
  urgent run, background vs normal at 80 %, catch-up once on power, "skip" and an expired
  window not caught up, watcher stops), `TestPowerSettings` (default, webhook not held, retry
  held in battery mode, turning it off releases at once, bad / corrupt values),
  `TestTaskFormPriority`, `TestPowerUI` (markup, wiring, runErr pattern against the core
  message, i18n both languages, label length). `newTestApp` pins the reading to "unknown" so
  other tests don't depend on the CI machine's battery.

**Inspiration**

- duplicati/duplicati `Duplicati/Library/Utility/Power/PowerSupply.cs`,
  `LinuxPowerSupplyState.cs` (Mains online → AC, no Mains at all → AC, all Mains offline →
  battery), `MacOSPowerSupplyState.cs` (`pmset -g batt` "'AC Power'" / "'Battery Power'", ioreg
  fallback), `WindowsPowerSupplyState.cs` (WMI `Win32_Battery`), and `Library/Main/Options.cs`
  `disable-on-battery` (only scheduled backups skipped, manual ones run, unknown = run).
  tidebot borrows the per-OS sources, "unknown never blocks" and "scheduled only"; it reads
  Windows through `GetSystemPowerStatus` instead of WMI (no COM, also gives battery saver), and
  adds the battery percentage / OS power saver tier, per-task priorities and the catch-up when
  power is back. Ideas only, no code copied.

**Learned**

- `agentctl tasks run` was recorded as a "cron" run; any rule keyed on the trigger (this one,
  `taskStillRunnable`) treated a hand-started run as scheduled.
- macOS 14 renamed `lowpowermode` to `powermode` (1 = low, 2 = high on some MacBook Pros);
  matching only "1" keeps High Power Mode from counting as a saver.
- Linux lists Bluetooth mice and headsets under `power_supply` as `type=Battery` with
  `scope=Device`; without that filter a desktop with a mouse looked like a laptop.

**Ideas for later**

1. Tray: a problem-area line "3 个任务在等电源" with "现在就跑" (one click releases them).
2. Don't start new runs right before sleep (macOS `IOPMAssertion` / Windows
   `PowerRegisterSuspendResumeNotification`), the other half of §12's "即将睡眠时不启动新运行".
3. Show the task priority as a small icon on the task list (bolt = urgent, leaf = background).
4. Let the low-battery threshold be chosen (10 / 20 / 30 %) next to the mode.

## 2026-10-26 (00:00): tasks move between computers — export to a JSON file, import with a preview

Category: **feature** (Tasks page, new API). The last rounds were power saving (cross-platform /
resource), notification delivery state (correctness) and budget rows (UI). A user with tidebot
on a Mac and on a Linux server had to retype every task (instruction, schedule, retries,
budget…) on the second machine; there was also no way to keep tasks outside the database.

**Done**

- `GET /api/tasks/export[?ids=…]` → `tidebot-tasks-YYYYMMDD.json` (date in the WebUI's time
  zone): `{kind: "tidebot.tasks", version: 1, exported_at, app, tasks: […]}`; each task keeps
  name, goal (only when it differs from the instruction), instruction, requirement texts,
  schedule, on/off, timeout, daily budget, per-run cap, model access *by name*, skill by id +
  name, allowed tools and run policy (retries, catch-up, success notice, priority).
  Never exported: working folder (a local path and personal; paths differ between OSes), task
  ids, run history, trigger / webhook tokens, notification channels, keys.
- `POST /api/tasks/import {file, apply, pick, enable}`: `apply=false` only checks and writes
  nothing; each row says the name it gets here and notes — `renamed` (a live task has the
  name; numbered "名 2" like Duplicate, also between rows of the same file),
  `provider_missing` (falls back to the default access), `skill_missing` (matched by id, then
  by name; else left unbound) — or the reason it can't be imported (same validation as the
  task form). `apply=true` creates the ticked rows; imported tasks are **paused** unless
  "turn schedules on as in the file" is ticked. A row that fails half way is archived, not
  left half made. File ≤ 4 MB, ≤ 200 tasks; a file from a newer format version is refused.
- Tasks page: Import / Export in the page head (export disabled with no tasks), Export in the
  batch bar for the ticked rows. Import dialog: drop zone or file picker (`.json`), pasting
  the file's text also works (remote desktop / SSH sessions); preview list with a checkbox per
  row, notes as small amber tags with the detail in the tooltip, the file's app version and
  export time in the tooltip of the count line; footer "导入 N 个任务" follows the ticks.
  Downloads go through the existing `downloadFile` (native Save dialog in the desktop app).
- Same on macOS / Windows / Linux, desktop window and browser: no paths in the file, BOM and
  CRLF tolerated on read, no shortcuts, no native dialogs; zh-CN and en; 1024×655 checked in
  both (scrollWidth 1024, footer button bottom at 595 px, the list scrolls inside 280 px).
- Shots: `/workspace/tidebot-shots/2026-10-26-task-import.png`, `…-task-import-en.png`,
  `…-task-export.png`, `…-task-export-en.png`.
- Tests: `TestTaskExportImport` (no folder / id leaks, all vs. picked export, unknown id 404,
  preview writes nothing, numbering across the DB and the file, missing provider / skill,
  invalid cron row, untitled row named from the first line, apply keeps constraints with new
  ids, tools, timeout, skill, policy, budget, paused by default, `enable` keeps on/off, bad
  files 400, CSRF 403), `TestNumberedName`, `TestTaskTransferUI`.

**Inspiration**

- n8n-io/n8n `packages/cli/src/commands/import/workflow.ts`: imported workflows are deactivated
  unless `--activeState=fromJson` is given → tidebot imports paused, with an opt-in checkbox.
- langgenius/dify `api/services/app_dsl_service.py`: the DSL carries `kind` + `version`
  (`check_version_compatibility`), secrets are left out of exports unless asked
  (`include_secret=False`), and `check_dependencies` reports plugins missing on the target
  ("leaked dependencies") → tidebot's `kind`/`version` header, no secrets or local paths at
  all, and the per-row `provider_missing` / `skill_missing` notes. Ideas only, no code copied.

**Learned**

- A task's skill binding stores only the id; ids differ per machine, so the export adds the
  skill name and the import matches by id first, then by name.
- Re-using `validateTaskForm` for each row kept import rules identical to the form (cron,
  limits, provider, skill), so the preview can't promise what the create would refuse.

**Ideas for later**

1. Bundle the bound skills into the export (a .zip with the JSON and each skill version) so a
   task with a skill moves in one file.
2. "Replace existing task with the same name" as an import mode (new version instead of a
   numbered copy), like Dify's overwrite import.
3. `agentctl tasks export / import` using the same format, for scripted setups.
4. Command palette entries "导出全部任务" / "导入任务".

## 2026-10-26 (04:20): same scrollbar, fonts and native controls on macOS / Windows / Linux

Category: **cross-platform / UI polish** (styles only). The last rounds were all features
(task export/import, power, notification state, budgets). Earlier layout checks ran in headless
Chrome, which hides scrollbars, so one thing was never seen: on Windows (WebView2 / Edge /
Chrome) and Linux (WebKitGTK, Chrome) the page scrollbar is a 15–17 px grey bar that comes and
goes with the page length. Measured at 1024×655 with real scrollbars: the content was 1024 px
wide on Tasks, Skills and Settings › Providers and 1009 px on Home, Runs, Plugins, Spend — every
switch between them moved the whole page 15 px sideways, the "跳动" we promised away.

**Done**

- One thin sand scrollbar everywhere: 8 px lane, transparent track, `--border-strong`-coloured
  pill thumb (2 px inset), darker on hover; `::-webkit-scrollbar` covers WKWebView / Safari,
  WebView2 / Chrome and WebKitGTK, `scrollbar-width: thin` + `scrollbar-color` only inside
  `@supports not selector(::-webkit-scrollbar)` (Firefox), because Chromium 121+ ignores the
  webkit rules on any element that has the standard properties.
- `html { overflow-y: scroll }`: the page always keeps its 8 px lane, so the content is 1016 px
  wide on every page in both languages (was 1024 / 1009), no horizontal overflow anywhere
  (checked Home, Tasks, Runs, Skills, Plugins, Spend, Settings, Providers, Budgets; zh-CN + en);
  the new-task dialog still fits (bottom 639, Create button at 614 of 655).
- `overscroll-behavior: contain` on dialogs, drawers, popovers, the command palette, the
  notification centre and the file / task lists inside dialogs: wheeling past the end of the
  palette list used to scroll the page behind by 521 px, now 0. The log and the amendment
  thread keep handing the wheel on to the page.
- `:root { color-scheme: light; accent-color: var(--accent) }`: native selects, date pickers
  and scrollbars stay light under a dark GTK / Windows theme, and checkboxes / radios are the
  tidebot blue instead of the GTK theme's orange or green.
- Font stacks: the English UI now has each OS's CJK face (PingFang SC / Hiragino, Microsoft
  YaHei UI, Noto Sans CJK SC / Source Han Sans / WenQuanYi) so a Chinese task name isn't drawn in
  a random fallback on Linux; colour emoji faces after `sans-serif` (digits never come from
  them); mono adds Cascadia Mono (Windows 11), DejaVu Sans Mono / Noto Sans Mono (Linux) and a
  CJK fallback for Chinese in logs.
- Shots (real scrollbars, `--hide-scrollbars` off): `/workspace/tidebot-shots/2026-10-26-scrollbars.png`,
  `…-scrollbars-en.png`, `…-scrollbars-run.png`, `…-scrollbars-run-en.png`, `…-scrollbars-modal.png`.
  flicker-check: same as before the change (CLS ≤ 0.002 on the settings tabs).
- Test: `TestCrossPlatformCSS` (webkit scrollbar rules, the Firefox properties only inside the
  `@supports` block and no `scrollbar-gutter`, the page lane not taken away by an html / body
  rule, color-scheme + accent-color, the overscroll list and that the log isn't in it, CJK +
  emoji faces in both UI stacks with emoji after the generic, the mono stack per OS ending in
  `monospace`, every `font-family` with a generic fallback, balanced braces).

**Inspiration**

- farion1231/cc-switch `src/index.css`: thin always-visible `::-webkit-scrollbar` with a
  padding-box thumb, the note that setting `scrollbar-width` / `scrollbar-color` makes WebView2
  ignore the webkit rules, and `.scroll-stable` using `overflow-y: scroll` instead of
  `scrollbar-gutter: stable` because WKWebView reserves no gutter for a styled scrollbar.
- CherryHQ/cherry-studio `src/renderer/assets/styles/scrollbar.css` (scrollbar colours as
  variables, transparent track / corner) and `src/renderer/assets/styles/font.css` (separate
  Windows stack with Microsoft YaHei UI, Cascadia Code for code, emoji faces at the end).
- open-webui/open-webui `src/app.css`: transparent `::-webkit-resizer` / scrollbar corner so
  textareas don't show a grey square. Ideas only, no code copied.

**Learned**

- Headless Chrome (Playwright adds `--hide-scrollbars`) lays pages out at the full 1024 px, so
  every earlier "fits 1024×655" check was 15 px more generous than a real Windows window.
  Screenshot scripts should launch with `ignore_default_args=["--hide-scrollbars"]`.
- The palette list leaked its wheel to the page only after reaching its end, which is exactly
  when a user keeps scrolling; `overscroll-behavior` fixes it without locking the body (locking
  would drop the scrollbar lane and shift the page again).

**Ideas for later**

1. Make `scripts/flicker-check.py` launch with real scrollbars and also report the content
   width per page, so a width jump fails the check.
2. The flicker check still sees ~60 skeleton frames on a skill detail page (also before this
   change): prefetch the skill on hover of its row, like the task rows.
3. Hide the thumb until the pointer is over a pane (Cherry Studio's `showOnHover`) for the
   small inner lists, keeping the page bar always visible.
4. Lock the page behind an open dialog without losing the lane (`overflow: hidden` +
   `padding-right: 8px` on html while `.modal-bg` is present).

## 2026-10-26 (08:40): open pages before the click — intent prefetch, read-only RPCs keep the page cache

Category: **performance (browser + server over a network)**. The last rounds were
cross-platform styles, task export/import and power; this one picks up idea 2 from 04:20
(skill detail loads late) and makes it general. On the same machine every request is ~1 ms so
nothing showed, but the server version is used over LAN / the internet, where each detail
page waited one round trip after the click, and the Plugins and skill pages emptied the whole
page cache because their read-only RPCs went out as POST and every POST counted as a write.

**Done**

- Intent prefetch: resting the pointer on an internal link or `[data-href]` row for 60 ms
  (mousemove restarts the clock, so sweeping across a list loads nothing), pressing it
  (`pointerdown` / `touchstart`, before the click lands), tabbing to it (`focusin`) or picking
  the row with j / k loads that page's first requests into the page cache. Covered: Home, Tasks,
  task detail (each tab, same paths as the render via `taskDetailPaths`), run detail, Skills and
  skill detail (including its three read RPCs). Addresses with filters (`?…`) are left out.
- The render takes a prefetched answer younger than 10 s as is (no second request); older ones
  are served and revalidated like any cached page. A 4xx refusal (e.g. "no canary to compare")
  is kept for the same 10 s so the page doesn't ask again; 5xx / network errors are dropped.
- Guards: only when signed in, tab visible, not the current page, not on Save-Data, at most 6
  in flight, never inside a dialog; keys the cache already holds (≤ 5 min) are skipped; a
  write while a prefetch is in flight (cache generation moved) discards its answer; a 401 from
  a prefetch doesn't open the sign-in screen (`quiet`).
- Read-only RPCs (`dashboard`, `plugins.list/catalog/get/config_get`, `triggers.list`,
  `backup(s).list`, `notify.overview`, `pricing.get`, `pricing.catalog.get`, `storage.get`,
  `power.get`, `network.get`, `skills.proposals/compare`, plus `skills.optimize_settings`
  without `mode` via `rpcRead`) no longer clear the page cache and are cached/deduped like GETs.
- Measured (Chrome, DevTools latency emulation, 1024×655): click → content with 150 ms latency
  task 204 → 94 ms, skill 195 → 65 ms, run 192 → 75 ms; with 400 ms latency ~800 → 70–150 ms
  (no skeleton). Back to Tasks after Plugins and a skill page at 400 ms: 795 → 20 ms.
  flicker-check unchanged (only the known skill-detail inner skeleton). No visual change, so no
  screenshot. Same code path on macOS / Windows / Linux, desktop window and browser (DOM events
  only, no shortcuts, no native dialogs, no new strings).
- Tests: `TestIntentPrefetch` (route → requests table incl. tab and encoded ids, skipped
  routes, every guard, render uses the prefetch without a request, stale prefetch revalidated,
  4xx kept / 5xx not, write race discards, read RPCs keep and share the cache, write RPCs drop
  it, in-flight cap released, no re-render from a prefetch); `TestListKeyboardCursor` checks j
  prefetches the picked row.

**Inspiration**

- sveltejs/kit `packages/kit/src/runtime/client/client.js` (`setup_preload`): `mouseover` arms a
  `mousemove` "pointer comes to rest" timer (20 ms), `mousedown` / `touchstart` preload at tap
  priority, no preload for the current URL, nothing when `navigator.connection.saveData`.
- open-webui/open-webui `src/app.html`: `<body data-sveltekit-preload-data="hover">` — the whole
  app opts into hover preloading. Ideas only, no code copied.

**Learned**

- Playwright's sync `page.route` with `time.sleep` serialises every request; use CDP
  `Network.emulateNetworkConditions` to add latency per request instead.
- The prefetch must not refresh keys the page cache already serves: the render returns the
  cached copy at once and relies on its own revalidation to repaint; a prefetch landing in
  between would update the cache silently and the page would stay stale.

**Ideas for later**

1. Prefetch the runs list too (needs the same URL builder `renderRuns` uses for its filters).
2. Server side: mark read-only RPCs in the registry and let the UI fetch the list, instead of a
   hand-kept `RPC_READS` array.
3. Viewport prefetch for the first few rows of Home's running list (SvelteKit's `viewport`
   priority), only on fast connections.

## 2026-10-26 (12:00): pause every schedule at once — for an hour, until tomorrow 9:00 or until resumed

Category: **feature / "everything from the WebUI" + cross-platform parity** (core scheduler,
Home, sidebar, command palette, desktop tray). The last rounds were performance (intent
prefetch), cross-platform styles and task export / import. "Pause all schedules" existed only
as a checkbox in the desktop tray: the server version (agentd, browser) had no way to do it at
all, the WebUI never showed that the tray had paused everything (Home kept listing "next 24
hours" runs that would not happen), and the pause had no end — easy to forget after a holiday.

**Done**

- Core: `scheduler.PauseUntil(on, until)` (zero = until resumed by hand); a timed pause ends
  by itself on the next tick and calls `OnAutoResume` once; `PauseState(now)` treats a pause past
  its end as over even before the tick clears it. Pause state behind a mutex (was an atomic flag).
- `internal/app/schedpause.go`: lengths `1h`, `4h`, `tomorrow` (09:00 next day in the
  scheduling timezone, DST-safe), `manual`; stored in setting `scheduler.pause` (end + start),
  restored at start-up for agentd and the desktop app alike; a timed pause that ran out while
  tidebot was off is over; the tray's old `desktop.scheduler_paused` flag moves over once.
  Pausing again keeps the start time. `Due` counts active tasks that came due meanwhile (same
  predicate as `ListDueTasks`). RPC `scheduler.pause.get` / `scheduler.pause.set` (WebUI and
  `agentctl call scheduler.pause.set '{"paused":true,"for":"1h"}'`).
- Nothing new on resume: due tasks follow each one's existing missed-runs setting (skip / once /
  all within the grace period). Runs by hand, buttons and webhooks are not affected.
- WebUI: a pause button in Home › Next 24 hours opens a small menu (each length shows its end,
  e.g. "今天 16:10 恢复"; arrow keys / Home / End / Enter / Esc, focus returns to the button);
  while paused the card shows one amber line "全部定时已暂停 · 明天 9:00 恢复 [立即恢复]" and
  dims the runs the pause holds back ("暂停中"); the sidebar shows an amber "定时已暂停" pill on
  every page (fed by the existing 30 s status poll — no new request), whose menu has "立即恢复"
  and "改为 …"; command palette: pause 1 h / until resumed, or resume. Start time, end and
  the due count are in the tooltip, not inline. Pausing toasts with Undo; resuming says how
  many due tasks will catch up or skip. A status answer that left before a change can't undo
  it (`spAdopt` keeps the newest).
- Desktop tray: the checkbox uses the same core call (pause until resumed); the tooltip says
  "Tidebot（已暂停，14:05 恢复）" for a timed pause from the WebUI; the tray menu follows a pause /
  auto-resume made elsewhere (state is in the tray signature).
- Fits 1024×655 (menu 280 px, flips above when there is no room below; sidebar pill collapses
  to its icon under 960 px); zh-CN + en strings; no native dialogs. flicker-check unchanged
  (only the known skill-detail inner skeleton). Shots: `/workspace/tidebot-shots/2026-10-26-pause-all.png`
  (+ `-menu`, `-sidebar`, `-en`).
- Tests: `TestPauseUntilEndsByItself` (scheduler: holds, ends once, past-end poll fires, manual
  never ends, resume clears), `TestSchedPauseEnd` (lengths, timezone, DST, bad input, tray
  time — runs on Windows too), `TestSchedPauseState` (persist + restart, expired, legacy flag,
  extend keeps start, due count, RPC, tray), `TestSchedPauseUI` (wiring, no native dialogs,
  i18n keys, dashboard field, node: newest answer wins, pill / tooltip text, escaping).

**Inspiration**

- louislam/uptime-kuma `server/model/maintenance.js` (`getStatus`: a "manual" maintenance
  is active until switched off, a single window is active between its start and end) and
  `src/pages/EditMaintenance.vue` (`setQuickDuration`: one-click 15 / 30 / 60 min lengths
  instead of typing an end time). Ideas only, no code copied.

**Learned**

- The demo binary embeds the static files: after a CSS/JS change, rebuild (`scripts/demo-webui.sh`)
  before taking screenshots, or the shot shows the old styles.
- `scripts/flicker-check.py` launches Playwright's bundled Chromium, which isn't installed on the
  box; running it with `channel="chrome"` works.

**Ideas for later**

1. Tasks page and task detail: show "下次 …" as held (and the sidebar pill's state) while paused.
2. Pause one task "until …" with the same lengths (today only pause / resume by hand).
3. Recurring quiet hours (e.g. no scheduled runs 23:00–07:00), Uptime Kuma's recurring windows.
4. `agentctl scheduler pause 1h | resume | status` as a friendlier wrapper over `call`.
5. A notification when a timed pause ends and N due tasks start catching up.

## 2026-10-26 (16:00): desktop tray idles for real — change-gated poll, light ping, indexed spend queries

Category: **resource usage** (desktop app idle footprint, SQLite query plans). The last rounds
were features (pause all, export / import) and UI (prefetch, cross-platform styles). The
desktop app sits in the tray all day, and its 2 s tray poll did the full tray model every
time: count running runs, `Cost.Summary` (today's spend per bucket plus one sum per budget),
count failures of the last hour, read the language setting, and ping the core with the IPC
`status` call (a DB count plus `runtime.ReadMemStats`, a stop-the-world pause). On a
database with 20k runs / ledger rows (a few months of hourly tasks) one tray model took
**13.9 ms** — 30 times a minute, with nobody looking.

**Done**

- `app.TrayRev(ctx, daemonErr, now)`: a cheap "might the tray look different?" token —
  SQLite `total_changes()` on the store's single connection (any write: runs, usage, budgets,
  settings incl. language), the Unix minute (the 1-hour failure window and the budget day
  roll over with time), liveness, the pause state and the last disk reading (both in memory).
  `app.TrayGate` remembers the last token; the desktop `pollTray` skips the tray model and the
  language read while it stays the same. A failed read gives a never-repeating token
  (recompute rather than go stale).
- New IPC `ping` (answers `{"ok":true}`: no DB, no ReadMemStats); `PingIPC` uses it. `status`
  is unchanged for `agentctl`. The daemon-down check stays at 2 s (detected in < 5 s).
- Migration 013: `idx_cost_created`, `idx_cost_task_created` on `cost_ledger` (every spend
  range query — budgets, today's spend, spend by task — filtered on `created_at` with no
  index and scanned the whole ledger) and `idx_runs_status_finished`; the tray's "failed in the
  last hour" query is split into two index ranges with the same `COALESCE(finished_at,
  created_at)` meaning.
- Measured (`BenchmarkTrayPoll`, 20k runs + 20k ledger rows, Linux x86): tray model 13.9 ms →
  1.1 ms; an idle poll is now `TrayRev` ≈ 4 µs + ping (handler 24 µs → 0.02 µs). Idle tray CPU
  per minute ≈ 420 ms → ~1–2 ms (one recompute per minute for the time window). The spend
  queries behind Home, the spend page and the pre-run budget check get the same indexes.
- No visible change, so no screenshot. Same code path on macOS / Windows / Linux (the tray poll
  is shared; `ping` goes over the unix socket or the Windows named pipe like any call).
- Tests: `TestTrayGate` (pure, runs on Windows too), `TestTrayRev` (reads and `TrayStatus` don't
  move the token; minute, daemon down, run start / fail, language, spend, pause / timed pause /
  resume / pause running out, disk low / freed all do), `TestTrayGateFollowsStatus` (30 idle polls
  compute once; every change reaches the tray on the next poll), `TestPingRPCIsLight` (pings
  through the real IPC server don't write), `TestTrayRecentFailuresWindow` (split query keeps the
  old semantics incl. NULL `finished_at`, stopped / succeeded excluded),
  `TestTrayQueriesUseIndexes` (EXPLAIN QUERY PLAN names the new indexes).

**Inspiration**

- farion1231/cc-switch `src-tauri/src/tray.rs` (`refresh_tray_if_problems_changed`,
  `refresh_tray_if_status_changed`): the tray status is recomputed on events (hover, the routing
  service starting / stopping) with a 1 s throttle and the menu rebuilt only when the collected
  status differs from `LAST_STATUS`. We already rebuilt only on change; the new part is not
  collecting at all unless a cheap token moved. Ideas only, no code copied.

**Learned**

- A liveness probe should be the cheapest call the server has: `status` looked harmless, but
  `ReadMemStats` stops the world and ran every 2 s.
- `COALESCE(a, b) >= ?` can't use an index; `a >= ? OR (a IS NULL AND b >= ?)` written as two
  `COUNT`s can (NULLs sit at the front of the index).
- `go test -bench` with a `testing.TB` app helper is enough to put a number on idle cost; the
  surprise (13.9 ms) came from a missing index, not from the poll rate.

**Ideas for later**

1. Drive the tray from the app's event bus (run started / finished, settings saved) and drop the
   2 s DB token check to a 30 s safety net; keep the 2 s ping only for liveness.
2. `rpcStatus` / Home `dashboard`: same token-gated cache server-side, so several open tabs share
   one `Cost.Summary` per change.
3. Audit the other `COALESCE(finished_at, created_at)` filters (runs list date ranges, task
   "last run") for the same split.
4. Ship `BenchmarkTrayPoll`-style numbers in CI as a guard (fail if idle tray poll > 1 ms).

## 2026-10-26 (20:00): pause one task for a while — it resumes by itself, and the next run says when "pause all" holds it

The 12:00 run let you pause every schedule for an hour or until tomorrow, but a single task
could only be paused until you remembered to resume it. And while "pause all" was on, the task
list and the task page still promised "下次 明天 3:00" for runs the pause would hold back.

**Done**

- Task page: **Pause** opens the same small menu as "pause all" (1 hour, 4 hours, until 9:00
  tomorrow, until resumed), each line showing when it would end; keyboard as before (arrows,
  Enter, Esc gives focus back). The toast has Undo. The menu code is now one `pzMenu` shared by
  both, so they can't drift apart.
- A timed pause is stored on the task (`pause_kind = 'snooze'`, end in `pause_detail`, no
  migration). The pill reads 限时暂停 / Snoozed, its tooltip gives the end and the start; the task
  list's schedule cell and the Overview's schedule row say "明天 0:08 恢复 · 下次 明天 18:00".
- The scheduler's poll calls a `Wake` hook first (also while every schedule is paused), which
  resumes tasks whose end has passed; a snooze that ran out while tidebot was off ends on the
  first tick. Waking (or resuming early) **skips** the fire times inside the snooze instead of
  catching up — a snooze means "not during this time". A plain pause by hand still follows the
  task's missed-run setting, unchanged.
- Snoozing again changes the end and keeps the start; "until resumed" turns it into a plain pause.
  An automatic pause (failures, budget, plugin, import review) can't be overwritten by a snooze
  (`ErrPausedOther`). WebUI goes through the new ext RPC `task.pause_for` (CSRF, owner session);
  `agentctl call tasks.pause {"id","for":"4h"}` does the same.
- While "pause all" is on, the task list's "下次 …" and the Overview's next run are dimmed with
  "· 暂停中" (tooltip: when the pause ends) if the pause holds that run back; repainted in place
  when the pause starts / ends (status poll or the menu), no reload. A task paused any other way
  shows "暂停中，不按计划运行" instead of a cron preview that would not happen.
- Tests: `TestTaskSnooze` (end / extend / manual / skip on wake and on early resume, a time still
  ahead kept, plain pause still catches up, automatic pause and archived tasks refused),
  `TestWakeRunsWhilePaused`, `TestTaskPauseFor` (RPC answer, bad length, agentctl form, wake
  during "pause all"), `TestTaskPauseUI` (static + node: held / not held around the pause end,
  manual pause holds all, escaping, snooze pill). Same Go and JS paths on macOS / Windows / Linux;
  times are RFC3339 UTC strings and shown in the scheduling timezone.
- Screenshot: `/workspace/tidebot-shots/2026-10-26-task-pause-for.png` (1024×655, task page with
  the menu open). README screenshots are unaffected (the Pause button only gained a caret).

**Inspiration**

- louislam/uptime-kuma `server/model/monitor.js` (`isUnderMaintenance`, the beat loop marks a
  check inside a maintenance window as MAINTENANCE rather than queueing it) and
  `server/model/maintenance.js` / `monitor_maintenance`: a maintenance window can target single
  monitors, and nothing is owed afterwards — hence "skip, don't catch up" for a snooze.
- louislam/uptime-kuma `src/pages/EditMaintenance.vue` (`setQuickDuration` 15 / 30 / 60 / 120 /
  240 min buttons) and `src/pages/Details.vue` (Pause on the monitor page): quick lengths instead
  of a date picker. Ideas only, no code copied.

**Learned**

- The "next run" a page shows is a promise; every pause (global, per task, automatic) has to be
  folded into it, or the UI says something the scheduler won't do.
- Reusing the free-text `pause_detail` column for the end time avoided a migration; comparing
  RFC3339 UTC strings in SQL is safe as long as every writer uses the same format.

**Ideas for later**

1. `agentctl tasks pause --for 4h` flag (today only `agentctl call`).
2. Snooze from the task list (row menu / `p` key in the j/k cursor) and the batch bar.
3. A notice when a snooze ends ("「Clean Logs」已恢复，下次 18:00"), opt-in per task.
4. "Skip next run only" as a fifth choice (advance `next_run_at` once, no pause).

## 2026-10-27 (00:05): daily tasks keep their hour — cron read in the scheduling timezone after every run

Category: **correctness** (the last rounds were features, resource usage and performance).
robfig/cron reads a schedule's fields in the zone of the `from` time it is given. The first
next run was computed from `time.Now()` (local), but after each scheduled fire the scheduler
passed `time.Now().UTC()`, and catch-up / snooze paths passed times parsed from the database
(UTC). So on a Shanghai machine "0 9 * * *" ran at 9:00 once and then at **17:00** (9:00 UTC)
from then on, while Home's "next 24 hours" and the cron preview still promised 9:00. Machines
set to UTC never saw it, which is why tests passed.

**Done**

- `Scheduler.SetLocation` / `Location`: the scheduler reads every cron in one zone (the
  configured scheduling timezone, `cfg.Location()`, the same one Home, budgets and the preview
  use). `NextRun` converts `from` into that zone before asking cron and returns the result in
  `from`'s zone, so every caller (fire, skip on overlap, missed runs, snooze wake, task create /
  edit / import, preview) agrees.
- `Scheduler.Realign` at core start: scheduled tasks whose stored next run is still ahead but is
  not the cron's next fire time in that zone get it recomputed (fixes rows written by the old
  code, and a changed timezone). Past-due times are left alone so the missed-run setting still
  decides; broken expressions are skipped. Logged per task.
- `time/tzdata` embedded in `internal/config`: Windows has no system zoneinfo, so a configured
  timezone or `$TZ` silently fell back to local time there; now it loads the same on macOS,
  Windows and Linux (~450 KB binary).
- No UI change (the times shown were already right; the runs now match them), so no screenshot.
- Tests: `TestNextRunReadsCronInSchedulerZone` (UTC / local / other-zone `from` all give 9:00
  Shanghai, result keeps `from`'s zone), `TestNextRunAcrossDST` (New York 9:00 stays 9:00 over
  the March change), `TestFireKeepsWallClock` (a real poll + fire stores the coming 9:00
  Shanghai, not 17:00), `TestRealign` (only the wrong future row changes; past-due, broken and
  manual tasks untouched; idempotent; a zone change moves future runs).

**Inspiration**

- n8n-io/n8n `packages/core/src/execution-engine/scheduled-task-manager.ts` (`register`): the
  timezone is part of the cron itself (`new CronTime(expression, timezone)`, `new CronJob(...,
  timezone)`) and of the registration key, so the next fire never depends on the zone of
  whatever "now" value is passed in, and a timezone change means re-registering. Ideas only, no
  code copied.

**Learned**

- With robfig/cron's default `time.Local`, `Next(t)` uses `t.Location()` — a UTC `from` is a
  different schedule. Bind the zone once and convert at the single entry point.
- Tests that only run on UTC CI machines can't see zone bugs; pin a non-UTC zone in the test.

**Ideas for later**

1. Show the scheduling timezone in Settings › General and allow changing it in the WebUI
   (Realign already handles the switch).
2. Per-task timezone (n8n has one per workflow), shown in the task form's cron preview.
3. Warn in the cron preview when a time falls in a DST gap / repeats (e.g. 2:30 on change days).
4. Route Home's "next 24 hours" and the budget runs-per-day estimate through `Sched.NextRun`
   too, so there is a single cron code path.

## 2026-10-27 (04:00): scheduling timezone can be seen and changed in Settings › General

Category: **UX** (the last round was correctness: cron read in the scheduling timezone).
The zone that decides when cron fires, when daily budgets reset and how every time is shown
could only be set in the config file or `$TIDEBOT_TIMEZONE`; Settings showed it read-only.

**Done**

- Settings › General › "调度时区 / Scheduling timezone": a select-looking button with the zone
  and its current UTC offset (local time in the tooltip) opens a searchable picker: "自动（跟随
  系统）" first (config file / `$TIDEBOT_TIMEZONE` / `$TZ` / system), then 419 IANA zones
  (zone.tab + zone1970.tab + UTC) sorted west to east, each with offset and local time, full
  date in the tooltip. Search matches name, region, `+8`, `UTC+08:00`, `GMT+8`; ↑↓ / PgUp /
  PgDn / Enter / Esc; opens on the zone in use. Picking saves at once; toast says how many tasks'
  next run moved. No native dialogs; fits 1024×655 (only the list scrolls).
- Core: `timezone.get` / `timezone.set` RPC (CSRF via `/api/rpc`), setting `schedule.timezone`
  (only names in the list; "" = automatic). Applies live: scheduler (`SetLocation` now atomic),
  budgets (`cost.LocFn`), quiet hours (`notify.LocFn`), WebUI (`Deps.LocFn`), tray, pauses; then
  `Scheduler.Realign` moves upcoming runs (past-due keep the missed-run rule). Persists across
  restarts. `/api/settings` gains `tz_offset`; offsets/local times come from the server's
  embedded tzdata so every WebView shows the same.
- Tests: `TestSchedulingTimezone` (list sorted, bad / unknown / "Local" refused, New York moves
  9:00 Shanghai to 9:00 New York, all subsystems switch, persisted, same zone moves nothing,
  back to automatic), `TestZonesLoad` (every zone loads, no duplicates), `TestTimezoneUI`,
  `TestTZOffsetMin` (DST, half-hour zone).

**Inspiration**

- louislam/uptime-kuma `src/components/settings/General.vue` and `src/util-frontend.js`
  (`timezoneList`): "Auto: <guessed>" as the first option, zones labelled `(UTC+08:00) Name` and
  sorted by current offset, server timezone separate from the browser's display zone. Ideas
  only, no code copied; search, local-time preview and realigning runs are tidebot's own.

**Learned**

- A runtime-changeable zone must be read through one getter (`App.Loc`) — four subsystems had
  each captured `cfg.Location()` at startup.
- Compute offsets on the server: `Intl` `longOffset` and zone data differ across older
  WebKitGTK / WebView2 builds.

**Ideas for later**

1. Per-task timezone in the task form (n8n has one per workflow).
2. DST gap / repeated-hour warning in the cron preview.
3. Route Home's "next 24 hours" and the budget runs-per-day estimate through `Sched.NextRun`.
4. `agentctl config timezone set <zone>` for headless servers.

## 2026-10-27 (08:00): a notification channel that keeps failing tells you — once, elsewhere

Category: **reliability / notifications** (recent rounds were scheduling, timezone, pauses,
tray, prefetch). Ideas 1 and 2 of 10-25 15:20: a channel's row showed "连续失败 3 次", but
only if you opened Settings › Notifications. A DingTalk robot that started refusing (keyword
check, rotated token) still failed silently until alerts were missed.

**Done**

- After `notify.DownAfter` (3) failed real sends in a row, one "通知渠道「运维群」连续 3 次发送
  失败" notice: in the bell (under 失败, error as the body), as a desktop notification and on
  every *other* channel that takes system messages — never on the failing one
  (`Message.SkipChannel`). One notice per outage, not per failure; when the channel delivers
  again, one "已恢复" notice. Tests from the settings page (and `agentctl`'s `notify.test`) move
  the streak but never raise either; changing the address / secret starts over.
- Red dot on Settings in the sidebar and on Settings › Notifications while an enabled channel's
  last send failed; the count is only in the tooltip ("1 个通知渠道最近一次发送失败"). The dot
  follows the list right after a test / save / switch (no extra request) and the bell's
  once-a-minute poll (`channels_failing` in `/api/notifications/center`). Collapsed sidebar:
  the dot sits on the icon.
- Bell entries for "channel down / back" and "low disk" open their settings page, laid out like
  the plain entries (title / body / time stacked) instead of the side-by-side link row.
- Core: migration `014_notify_channel_down.sql` (`down_notified` flag; channels that already
  get system messages get rules for `channel_down` / `channel_up`). The streak update uses
  `UPDATE … RETURNING`, the flag flip is guarded (`WHERE down_notified=0`) so two sends failing
  at once raise one notice. Errors in the notice are the already-cleaned ones (no URL tokens).
- Same on macOS / Windows / Linux, desktop and browser: plain CSS dot, no paths, no native
  dialogs. zh-CN and en for every string (server and WebUI); 1024×655 checked (scrollWidth 1024).
- CI: the Linux desktop job's `apt-get install` hit the Azure Ubuntu mirror at ~40 KB/s twice
  (20 min job timeout; run of 0f7acba was cancelled, a rerun passed). `scripts/ci-apt-install.sh`
  now caches the .debs between runs (actions/cache), puts 20 s timeouts / 3 retries on every
  request and, if the install still stalls past 7 min, retries once against
  archive.ubuntu.com. Used by full-matrix and release.
- Shot: `/workspace/shots/notify-channel-down-1024x655.png`.
- Tests: `TestChannelDownUp` (threshold, one notice per outage, tests stay quiet, recovery,
  new address resets, SkipChannel, FailingCount ignores disabled), `TestChannelDownReachesOthers`
  (bus event → message in the viewer's language → other channel only, bell row, recovery),
  `TestChannelDownCenter`, `TestChannelDownUI`.

**Inspiration**

- louislam/uptime-kuma `server/model/monitor.js` (`isImportantForNotification`: notify on
  PENDING→DOWN and DOWN→UP only; retries before DOWN): one notice per outage after a few
  failures, one when it is back. healthchecks/healthchecks `hc/api/models.py` (`Channel.notify`
  keeps `last_error` and flips `disabled` on permanent errors) for keeping the state on the
  channel row. Ideas only, no code copied.

**Learned**

- A notice about a broken channel must not be routed through the normal rules unfiltered, or
  it goes to the very channel that is broken (and counts as one more failure).
- "Test" sends need their own path end to end; the old `notify.test` RPC called `Send` and would
  have raised outage notices for a CLI test.

**Ideas for later**

1. Offer "停用此渠道" on the "连续失败" notice / row when the error is permanent (WeCom "invalid
   webhook key", DingTalk "token is not exist"), Healthchecks-style, without doing it silently.
2. A small per-channel history (last 10 sends) in the edit dialog.
3. Let the threshold (3) be chosen per channel next to the groups.
4. Resend the notices a channel missed while down, once it recovers (opt-in, capped at 10).

## 2026-10-27 (12:00): runs list exports what it shows — CSV for a spreadsheet, JSONL for scripts

Category: **feature** (recent rounds: notifications, timezone UX, cron correctness, pauses,
tray resource use). Idea 2 of 10-24 09:20: the runs list could be filtered by status, task,
search text and date, but the only way to get runs out was the spend page's CSV (cost rows,
no status filter, no errors or prompts) or copying pages by hand.

**Done**

- Runs page header: "导出 / Export" (ghost button with a caret, next to the date range). Its
  menu says how many runs and which filters ("导出 9 条运行 · 失败 · 最近 7 天 · 搜索“x”"), then
  CSV ("Excel、Numbers 可直接打开") and JSONL ("每行一个 JSON，便于脚本处理"); the note in the menu
  covers the rest (all matching runs, not just the loaded pages; CSV in the scheduling zone,
  JSONL in UTC; amounts in the display currency). Arrow keys / Home / End / Enter / Esc;
  disabled while the list is empty; its tooltip hides while the menu is open. Goes through the
  shared `downloadFile` → native "Save as" in the desktop app, a download in a browser.
- `GET /api/runs/export?format=csv|jsonl&lang=` takes exactly the list's query (`runsFilter`
  + `runsOrder`, both now shared with `/api/runs`), up to 100,000 runs. CSV: BOM, 20 localized
  columns (date, start, finish, task, run id, status, trigger, provider, model, version,
  seconds, requests, 4 token counts, amount, currency, error on one line, prompt),
  formula-safe cells (`csvSafe`). JSONL: English keys, stored UTC times, error kept as is.
  Prompts are cut at 4000 bytes and errors at 2000 on a rune boundary (the file is built in
  memory so the single DB connection is not held by a slow reader). File name follows the
  range: `tidebot-runs_2026-10-21_to_2026-10-27.csv`, one day → that day, none → `all_to_<today>`.
- `popMenu` pulled out of the pause menu (`pzMenu` now builds its items and calls it), so
  every small menu has the same placement and keys.
- Same on macOS / Windows / Linux, desktop and browser: no paths, no native dialogs, both
  languages, scrollWidth 1024 at 1024×655 (zh-CN and en checked).
- Tests: `TestRunsExport` (order, localized cells, formula guard, times, clipping, every
  filter + sort, file names, JSONL, 400s / 405), `TestClip`, `TestRunsExportUI`.
- Shots: `/workspace/screens/runs-export-menu-1024x655.png`, `…-en-1024x655.png`.

**Inspiration**

- langfuse/langfuse `web/src/components/BatchExportTableButton.tsx` (an icon export menu on
  each table that sends the table's filter, search and order, one entry per format) and
  `packages/shared/src/features/batchExport/types.ts` (CSV / JSON / JSONL with their MIME
  types). Langfuse queues a job and emails a link; a single-user app can answer at once.
  Ideas only, no code copied.

**Learned**

- Pull the WHERE and ORDER BY out of the list handler before adding a second consumer —
  otherwise the export and the list drift (a new chip that only one of them knows).
- Excel reads a lone `\n` inside a quoted cell fine but row heights explode; fold errors to
  one line in CSV and keep them exact in JSONL.

**Ideas for later**

1. "Export selected" once the runs list has row ticks (x in the j/k cursor).
2. Command palette entry "导出当前运行列表".
3. `agentctl runs export --status failed --range 7d --format jsonl` over the same query.
4. Include each run's log (zip of CSV + logs) for a chosen few runs, for bug reports.

## 2026-10-27 (16:00): skill files in Markdown open formatted — preview / source switch

Category: **UI polish / reading** (recent rounds: runs export, notification outages, timezone
settings, cron correctness, task snooze, tray idling). Idea 3 of 10-25 00:20: the skill file
browser showed SKILL.md and its reference pages only as raw text with line numbers — front
matter, tables and fenced commands read like source code, and links between the pages did
nothing.

**Done**

- Skill page › 文件: `.md` / `.markdown` files open as formatted text; a small "预览 / 源码"
  switch in the file header (arrow keys too) goes back to the numbered source. The choice is
  remembered for every Markdown file (`tb_md_view` in localStorage); copy always copies the
  source. Hints on the two buttons say external images are not loaded.
- Renderer (`mdHtml`, no library, ~150 lines): YAML front matter as a compact key / value table
  (folded `>` / `|` values joined), ATX + setext headings, paragraphs with hard breaks,
  emphasis / strong / strike, code spans and fenced or indented code (language label top
  right), block quotes, nested and task lists, GFM tables with alignment, rules, backslash
  escapes, CRLF files.
- Links: http(s) / mailto open in a new tab (address in the tooltip); a relative link such as
  `reference/fields.md` or `../x.md` resolves against the file's folder and opens that file in
  the viewer (its folders unfold, the tree row is selected); `#heading` scrolls within the
  pane (GitHub-style slugs, no element ids on the page); a link to a file the version doesn't
  have says so in a toast. Everything else (javascript:, data:, `//host`, absolute paths)
  stays plain text. Images become a "🖼 alt" link — nothing is fetched while reading.
- Safety: the file's text is escaped before any markup is added and only a fixed set of tags
  comes out; raw HTML in the file shows as text. No lookbehind in the regexes (an older WebKit
  in the desktop app would reject the whole script).
- Look: sand palette, 14 px body / 8px grid, headings with a hairline, code blocks on the same
  `#fbfaf6` as the source view; the pane scrolls on its own inside the 1024×655 layout
  (scrollWidth 1024 in zh-CN and en). Same on macOS / Windows / Linux, desktop and browser.
- Tests: `TestMarkdownPreviewUI` (wiring, strings in both languages, styles, no native dialogs
  / innerHTML / lookbehind in the block) and `TestMarkdownRender` (runs the renderer in node:
  front matter, lists, tables, fences, setext, link resolution, autolinks, XSS cases).
- Shots: `/workspace/tidebot-shots/skill-md-preview-1024x655.png`, `…-en-1024x655.png`.

**Inspiration**

- siteboon/claudecodeui `src/modules/code-editor/CodeEditor.tsx` (Markdown files get a
  preview / edit toggle in the editor header, `CodeEditorHeader.tsx`) and
  `src/modules/code-editor/markdown/MarkdownPreview.tsx` (GFM tables, task lists, links in a
  new tab with `noopener noreferrer`). They use react-markdown; tidebot keeps a small escaped
  renderer instead of adding a dependency. Front matter as a table follows GitHub's rendering.
  Ideas only, no code copied.

**Learned**

- Bare-URL linking must not run the label back through the inline pass (it re-matches itself
  and recurses forever) — autolinks get an escaped label.
- Placeholders for code spans / links before escaping keep the order of passes simple and make
  "escape everything, add known tags" easy to check.

**Ideas for later**

1. Same preview for the task prompt and the run's final answer (agents answer in Markdown).
2. Rendered diff for Markdown in the compare tab (changed blocks highlighted, not lines).
3. Outline (h1–h3) in a small popover for long references, Open WebUI-style.
4. Search inside the open file (⌘/Ctrl+F in the pane, n / N), shared with the log view.

## 2026-10-27 (20:00): run page shows the agent's final answer, formatted

Category: **logic / data + reading** (recent rounds: skill Markdown preview, runs export,
channel outages, timezone, cron fix, task snooze). Idea 1 of 10-27 16:00: the final answer of
a run — what a scheduled task is actually for — was only in the log as one `result:` line,
newlines flattened and cut at 400 characters. Nothing kept the full text.

**Done**

- Runner: the stream's final `result` text is stored with the run (`runs.result_text`,
  migration 015), CRLF folded to LF, capped at 128 KB on a UTF-8 boundary
  (`result_truncated` set). The API key is masked the same way as in the log; an error result
  is not stored twice (it already is the run's error message).
- API: `GET /api/runs/{id}` returns `result` / `result_truncated`; the runs list and export
  don't carry it, so lists stay small.
- Run page: a "最终回答" card under the stat cards with "预览 / 源码" (own remembered choice
  `tb_ans_view`, arrow keys too), copy the raw text, and download as `<run id>.md` (system
  save dialog in the desktop app). Line count and size in the title's tooltip; a "已截断" pill
  with a tooltip when cut. The body scrolls inside the card (max 320 px / 40 vh, overscroll
  contained) so the log stays reachable at 1024×655. Runs from before this have no card.
- Renderer: `mdHtml(text, null)` is the answer mode — no front-matter table (a leading `---`
  is just a rule) and relative links stay text (there are no files to open); web links open in
  a new tab and `#heading` links scroll inside the card. Same escaping as the skill preview.
- Tests: `TestCapRunResult`, `TestRunResultStore` (cap, UTF-8, quotes stay data),
  `TestRunOnceWithMock` (answer stored end to end), `TestRunResultMaskedAndErrorsNotStored`,
  `TestRunAnswerAPI` (detail yes, list no), `TestRunAnswerUI`, `TestRunAnswerRender` (node).
- Shots: `/workspace/tidebot-shots/run-answer-1024x655.png`, `…-en-1024x655.png`.

**Inspiration**

- cline/cline `apps/vscode/webview-ui/src/components/chat/CompletionOutputRow.tsx`: the turn's
  last response sits in its own quiet container with a small label and a copy button, rendered
  through `MarkdownRow`, separate from the tool chatter above it. tidebot does the same on the
  run page, with the existing escaped renderer. Ideas only, no code copied.

**Learned**

- The log is a view, not a store: anything a later screen needs (the answer) has to be saved
  as data when the event arrives — the formatter already threw the newlines away.
- One renderer, two contexts: a `null` base was enough to switch off what only makes sense
  inside a skill (front matter, file links) without forking the code.

**Ideas for later**

1. "成功时通知" sends the first lines of the answer (feishu / DingTalk / WeCom take Markdown).
2. Compare a task's last two answers (rendered diff), e.g. to see what changed in a digest.
3. Search runs by answer text (FTS5 index on `result_text`), plus `agentctl runs show --answer`.
4. Full prompt on the run page: the detail still shows the list's 300-char preview.

## 2026-10-28 (00:00): success notices carry the start of the answer

Category: **notifications / correctness** (recent rounds: run final answer, skill Markdown
preview, runs export, channel outages, timezone, cron fix). Idea 1 of 10-27 20:00: "成功时也通知"
only said "运行完成：日报 · 花费 $0.01" — for a daily digest task the answer *is* the point, yet
the group chat got none of it and the bell couldn't even open the run.

**Done**

- When a run succeeds, the stored (key-masked, capped) final answer rides on the event. The
  notice for chat robots (Feishu, DingTalk, WeCom, Telegram), the bell and the webhook gets a
  plain-text excerpt: Markdown marks stripped (headings, emphasis, code fences, quotes, table
  rules; bullets → "•", task boxes → ☐/☑, web links keep their address), at most 8 lines /
  360 characters, "…" plus "完整回答见 tidebot 运行详情" when cut. Far below WeCom's 2048-byte
  text limit even in CJK.
- Email gets the whole answer. Mail bodies are now quoted-printable with CRLF line ends and an
  RFC 2047 subject, so long lines (past SMTP's 998 octets) and any script arrive intact.
- Desktop notification: cost plus the answer's opening (120 characters).
- Per task, "附带回答开头" under "成功时也通知" (on by default, tooltip says what goes where and
  to turn it off for sensitive answers); folded summary reads "成功也通知（附回答）".
  `task_policies.notify_answer` (migration 016). A form, exported task file or
  `agentctl call policy.set` without the field keeps the task's choice.
- Bell: run notices (finished / failed / interrupted) open their run page; the excerpt shows in
  a quiet three-line block, the rest on hover.
- Daily digest folds each notice to one line of ≤ 200 characters (an excerpt would otherwise
  fill it).
- Tests: `TestAnswerExcerpt`, `TestDigestLine`, `TestBuildMailLongAnswer` (QP round trip, line
  length, encoded subject), `TestSuccessNoticeCarriesAnswer`, `TestPolicySetKeepsNotifyAnswer`,
  `TestTaskFormNotifyAnswer`, `TestNotifyAnswerUI`.
- Shots: `/workspace/tidebot-shots/2026-10-28-notify-answer-bell-1024x655.png`,
  `…-form-1024x655.png` (and `-en`).

**Inspiration**

- healthchecks/healthchecks `hc/api/transports.py` (`get_ping_body(ping, maxlen)` appends
  "[truncated]") with `hc/integrations/telegram/transport.py` and
  `hc/integrations/slack/transport.py` (chat messages clip the last ping body at 1000 chars
  "for consistency") and `hc/integrations/email/transport.py` (email carries the whole body).
  tidebot applies the same split to a run's answer. Ideas only, no code copied.

**Learned**

- A chat excerpt should be flattened, not just cut: raw `**`, `|---|` and fences read worse in
  a WeCom text message than a shorter plain line.
- `smtp.SendMail` dot-stuffs but doesn't wrap lines; any user-sized text in mail needs a
  transfer encoding.
- A new boolean that defaults to *on* needs "absent means keep" on every writer (form, import,
  RPC), or older callers silently switch it off.

**Ideas for later**

1. Markdown message types for DingTalk / WeCom / Feishu cards (headings and links rendered).
2. A public base URL setting so channel messages can link straight to the run page.
3. Per-channel choice of excerpt vs. full answer (e.g. full text to one email, excerpt to chat).
4. "Only notify when the answer changed" for digest-style tasks (compare with the last answer).

## 2026-10-28 (04:00): provider models picked from the provider's own list

Category: **settings / model providers — UI interaction** (recent rounds were all run answers and
notifications). The five model fields in a provider drawer were free text: you copied ids such as
`deepseek-v4-flash` or `qwen3-coder-plus` from a docs page, and a typo only showed up when a
run failed.

**Done**

- `POST /api/providers/{name}/models` asks the provider's `GET …/v1/models` with the saved key
  (Bearer or x-api-key as configured, `anthropic-version` set). Gateways that serve Anthropic
  under a sub-path (`/anthropic`, `/api/anthropic`, `/apps/anthropic`, `/coding`, `/claude`)
  fall back to the root `/v1/models`, `/models` (and DashScope's `/compatible-mode/v1/models`)
  with Bearer — only on 404/405; a refused key or rate limit is the answer. Anthropic paging
  (`has_more` / `after_id`, ≤ 10 pages) is followed. Reads `data[]` (id, display_name,
  owned_by, created / created_at) and `models[]` (slug / id / name); odd ids and duplicates
  dropped, ≤ 500, newest first when dated, else by id. POST so it passes the CSRF check; the
  key is masked out of every error.
- Kept in memory 10 minutes per provider, keyed by base URL + auth + key hash; saving, renaming
  or removing the provider drops it; the button fetches again.
- Drawer: "获取模型列表" at the right of the Models heading (disabled until saved, like Check).
  The first focus on a model field fetches by itself when a key is saved. Each field becomes a
  combobox: the list opens under it (upwards near the footer), typing filters with the match
  in bold, ↑/↓/PageUp/PageDown + Enter picks, Esc closes the list before the drawer, any other
  name can still be typed. Display names show muted, owner on hover. After a fetch the button
  reads "10 个模型" and its tooltip says where from and when.
- A filled-in model not on the fetched list gets a quiet amber note ("不在该接入的 10 个模型中，
  可点「检测」确认"); a Check result in the same slot wins.
- No list (all candidates 404) says so once on the button tooltip without a toast on auto-fetch.
- Tests: `TestModelListURLs`, `TestParseModelList`, `TestListModelsFallbackCacheAndSecrets`,
  `TestListModelsPagesAndUnsupported`, `TestProviderModelsRoute` (CSRF), `TestProviderModelPickerUI`.
- Shots: `/workspace/tidebot-shots/2026-10-28-provider-models-1024x655.png`,
  `…-provider-models-haiku-1024x655.png` (and `-en`).

**Inspiration**

- farion1231/cc-switch `src-tauri/src/services/model_fetch.rs` (candidate URLs: strip known
  Anthropic-compat suffixes, try `/v1/models` then `/models`, move on only on 404/405, redact
  the key in error bodies; parse `data[]` or `models[].slug`) and
  `src/components/providers/forms/shared/ModelInputWithFetch.tsx` / `ModelDropdown.tsx` (a
  fetch button beside the model input that turns into a searchable dropdown; the input stays
  free text). Ideas only, no code copied.

**Learned**

- One shared popover for several inputs needs every blur/input handler to check it still owns
  the popover, or the field you leave closes the list the next field just opened.
- The drawer's Esc listener runs in the capture phase, so an inner list can't stop it with
  `stopPropagation`; the drawer has to look for an open list itself.

**Ideas for later**

1. Offer the list in the "add provider" drawer before saving (send the typed key once, not stored).
2. Use the fetched list in the task form / quick run to pick a per-task model override.
3. Pricing editor: add rows straight from the fetched models that have no price yet.
4. Show the list's model count on the provider card tooltip, and flag cards whose saved models
   went missing from the provider's list.

## 2026-10-28 (08:00): run page shows the whole prompt — and "rerun" reruns all of it

Category: **correctness** (recent rounds were features: provider model list, answer excerpts,
final answer card). Idea 4 of 10-27 20:00 turned out to hide a real bug: every run query cut the
prompt to its first 300 *bytes* plus "…", including the one behind the run page and the rerun
endpoint.

**Done**

- 重跑临时运行时用完整提示词：`POST /api/runs/{id}/rerun` 以前从列表查询取提示词，超过 300 字节的
  只重跑了开头加「…」。运行详情、「编辑后重跑」「存为任务」同样拿到的是截断版。现在列表仍只带
  预览（`queryRuns`），详情和重跑用 `queryRunsFull`。
- 截断不再切坏中文：新包 `internal/textclip`（`Head` / `Ellipsis`）按字符边界截字节数。替换了
  运行列表预览、技能描述（1024 字节，存进库里的是坏字节）、更新说明、服务商检测/模型列表的错误
  信息和模型名、自动推断的输入、运行导出。
- 服务商错误先打码再截断：以前先截 300 字节再去掉密钥，恰好跨在截断处的密钥会留下一段前缀。
- 运行页详情卡的提示词：保留换行，超过 4 行折叠并渐隐，「展开全部」或点文字展开（超过 40vh
  卡内滚动），可复制完整提示词；字数和行数在标签的悬停提示里。只在确实溢出时出现按钮
  （挂载后测量，宽度变化时用 ResizeObserver 重算），短提示词和以前一样。
- Tests: `TestHead`, `TestEllipsis`, `TestRunPromptFull`（列表预览是合法 UTF-8、详情完整、
  重跑拿到完整提示词）, `TestRunPromptUI`, `TestMaskBeforeClip`.
- Shots: `/workspace/screenshots/2026-10-28-run-prompt-folded-1024x655.png`, `…-open-…`（及 `-en`）.

**Inspiration**

- cline/cline `apps/vscode/webview-ui/src/components/chat/task-header/TaskHeader.tsx`：任务原文
  `whitespace-pre-wrap`、限高 4.5rem 并用渐变遮罩淡出，`scrollHeight > clientHeight` 判断是否
  需要折叠，点击展开到 25vh 内滚动，旁边一个复制按钮。tidebot 照这个思路做了运行页提示词。
  只借鉴思路，未复制代码。

**Learned**

- 「列表只要预览」的截断放在共用查询里，后来每个复用它的接口都悄悄拿到了截断版——截断应该是
  调用方的选择，不是查询的默认。
- Go 里 `s[:n]` 对中文是按字节切的；JSON 编码把坏尾巴变成 U+FFFD，SQLite 则原样存下坏字节。
- 打码和截断的顺序会影响安全：先打码。

**Ideas for later**

1. 任务详情页的提示词也用同样的折叠/展开（现在是整段平铺的就地编辑框）。
2. 运行列表搜索提示词时，预览里高亮命中的位置（命中在 300 字节之后时显示命中附近的片段）。
3. `agentctl runs show <id>` 输出完整提示词和最终回答。
4. 全仓检查其余 `[:n]` 字节截断（日志字段、插件输出），统一走 `textclip`。

## 2026-10-28 (12:00): finished run logs are kept gzipped

Category: **resource usage (disk)** (the last rounds were correctness and features; the last
resource round was the tray on 10-26). Every run writes a human-readable `runs/<id>.log`, kept
30 days by default and never compressed — an hourly task fills the folder with hundreds of
near-identical text files a month.

**Done**

- `runner.CompressFinishedLogs`: the retention tick (at start-up, then every 6 h, after the
  cleanup so nothing about to be deleted gets packed) gzips the logs no run writes to any more:
  not in flight in this runner, not queued/running in the database, untouched for 10 minutes.
  Best-compression gzip, written to `<id>.log.gz.tmp`, synced, renamed, then the plain file is
  removed; the modification time is copied over (retention counts days from it). Files under
  4 KB (one filesystem block, nothing to free) and ones that wouldn't shrink stay as they are.
  If the plain file can't be removed (Windows: a reader still has it open) the copy is dropped
  and the next sweep tries again; temp files left by a crash are removed after an hour.
- Readers don't care which form is on disk: `runner.OpenLogFile` / `ReadLogFile` prefer the
  plain file, else the `.gz`. The run page's SSE log streams a packed log in one pass with the
  same line ids (offsets into the original text), so a reconnect with `Last-Event-ID` / `?from=`
  still continues where it left off; the AI-plugin job view reads it the same way. Retention,
  storage usage and backups count `.log.gz` (and leftovers) under the right run id.
- Settings › Data › Storage: the "run logs" tooltip adds "其中 N 个已结束运行的日志已压缩：原始 →
  现在" (raw size from the gzip trailer, no decompression). zh-CN and en.
- Demo (9 runs whose logs carry Read tool output of Go source, the less compressible case):
  239.7 KB → 64.6 KB. Plain run logs (timestamps, tool calls, repeated prefixes) shrink further.
  Same code on macOS / Windows / Linux (pure Go `compress/gzip`, no new dependency).
- Tests: `TestLogRunID`, `TestCompressLogFileRoundTrip` (CJK + CRLF bytes identical, mtime kept,
  ISIZE), `TestCompressLogFileLeavesSmallAndRandom` (plain wins when both exist),
  `TestCompressFinishedLogsSkipsLiveBusyRecent`, `TestRunEventsPackedLog` (same stream as the plain
  file, resume by offset), `TestRetentionTickPacksFinishedLogs`, `TestStoragePackedTipUI`.
- Shots: `/workspace/tidebot-shots/2026-10-28-storage-packed-logs-1024x655.png` (and `-en`).

**Inspiration**

- jhuckaby/Cronicle `lib/job.js` (`uploadJobLog`: a finished job's `log.txt` is piped through
  `zlib.createGzip` into storage as `jobs/<id>/log.txt.gz`, the local file removed afterwards) and
  `lib/api/job.js` (`api_get_job_log` serves the stored `.gz` as is with `Content-Encoding: gzip`).
  tidebot keeps the live file plain and packs only once the run is done and quiet. Ideas only, no
  code copied.

**Learned**

- Windows can't delete a file another handle has open (Go doesn't open with
  `FILE_SHARE_DELETE`), so "compress, then remove" must be able to back out and retry later
  instead of leaving two copies.
- Keeping SSE ids as offsets into the *original* text made the packed path a drop-in: the client
  never learns the file changed form.
- A gzip trailer's ISIZE gives the original size for free; good enough for a usage tooltip.

**Ideas for later**

1. Serve "download log" (a future button on the run page) straight from the `.gz` with
   `Content-Encoding: gzip`, like Cronicle.
2. Pack right after a run ends (with the 10-minute quiet delay on a timer) instead of waiting for
   the 6-hourly tick.
3. Compress artifacts that are plain text (reports, exports) the same way, or store them in a
   per-run zip.
4. Show "freed by compression" next to the last cleanup in the storage tooltip.

## 2026-10-28 (16:00): runs search shows where it matched; search boxes wait for the input method

Category: **UX / interaction** (the last rounds were disk usage, correctness and features).
Searching the runs page matched the full prompt, the error, the task name and the id in SQL, but
the rows only showed "task #id" and the trigger — a hit 600 bytes into a prompt left no trace,
so you couldn't tell why a row was there. And every search box searched while you were still
typing pinyin ("tianqi" before 天气), while Enter / Esc that only confirm or cancel an IME
candidate also ran our shortcuts.

**Done**

- 运行搜索显示命中片段：`GET /api/runs?q=` 每行带 `match`（命中字段 prompt / error / task / id、
  命中次数、片段）。按完整提示词匹配（列表本身仍只带 300 字节预览），按 SQLite LIKE 的规则只折叠
  ASCII 大小写，片段从命中前 8 个字符（不跨到上一行）到命中后 80 个字符，按字符边界截断，空白
  （含 CRLF、Tab）压成一个空格，片段内每处命中都标出，截断处加「…」；片段按「普通/命中」交替的
  数组返回，页面逐段转义，不用再在前端重新查找。
- 运行列表：有搜索词时，标题下的触发方式行换成「提示词 · …片段…」，命中处高亮（与日志搜索同色），
  命中字段和次数在悬停提示；只在任务名命中时不出片段，改为高亮标题。
- 输入法：`window` 捕获阶段统一拦下输入法组字中的按键（`isComposing` 或 Safari 的 keyCode 229），
  用回车选词不再执行命令面板第一项、不再提前保存就地编辑的名称，Esc 取消候选不再关掉弹窗/抽屉。
- 搜索框改用 `imeInput`：组字期间不搜索，选词结束（compositionend）立即搜一次，清空立即生效，
  其他输入按原来的防抖；同一内容不重复请求。用于运行、任务、日志搜索，命令面板，服务商选择、
  时区选择和图标搜索。
- Tests: `TestFindRunHit`（字段顺序、ASCII 折叠、多处命中、跨行、按字符截断、跨窗口的命中保持
  完整、次数上限）, `TestRunsSearchHit`（超过预览长度的命中、% 和 _ 按字面、无搜索词不带 match）,
  `TestRunsHitUI`（node 跑 `runHitHtml` 转义和 `imeInput` 组字/选词/防抖/清空）。
- Shots: `/workspace/tidebot-shots/2026-10-28-runs-search-hit-1024x655.png`（及 `-en`）。

**Inspiration**

- CherryHQ/cherry-studio `src/main/utils/searchSnippet.ts`（命中前后各取一段、合并区间、截断处
  加 "..."）和 `src/renderer/components/HighlightText.tsx`（片段里标出关键词）；
  `src/renderer/components/GlobalSearch/useImeAwareDebouncedValue.ts`（组字时冻结、compositionend
  立即提交、清空同步提交）。cline/cline `apps/vscode/webview-ui/src/components/history/HistoryView.tsx`
  （历史列表按命中区间高亮，`ignoreLocation` 的注释正是"命中在 60 字符之后就被漏掉"的同类问题）。
  只借鉴思路，未复制代码。

**Learned**

- 搜索命中的位置只有服务端知道（列表只有预览），所以片段要在服务端切；按「普通/命中」交替返回
  比返回偏移量更稳：Go 的字节偏移、JS 的 UTF-16 下标不用互相换算。
- SQLite LIKE 只对 ASCII 不分大小写；片段用同样的规则，才不会出现「列出来了但标不出命中」。
- 单元格大约只放得下 16 个汉字：命中前的上下文要短，否则命中被省略号吃掉。
- 输入法的回车在 Safari 上 `isComposing` 已经是 false，只能靠 keyCode 229 识别。

**Ideas for later**

1. 运行详情页从搜索进入时（带 `?q=`），提示词卡自动展开并高亮命中，日志搜索框预填同一个词。
2. 全局搜索（顶栏 / 命令面板）的运行结果同样显示命中片段。
3. 任务列表搜索提示词时也给片段（现在只按名称在前端过滤）。
4. 多个词（空格分隔）按 AND 搜索，每个词分别高亮。

**Follow-up (same round)**: CI on macOS caught a flaky `TestCanaryCompareAndDone` — `optimize.nowUTC`
used RFC3339Nano, which drops trailing zeros, so `created_at` compared as text could put
"…:14.1Z" after "…:14.12Z" and the canary's own runs fell before its start. Timestamps there are
now fixed width (`TestNowUTCSortsAsText`). Idea: grep the rest of the repo for RFC3339Nano values
that are compared or sorted as text.

## 2026-10-28 (20:00): daily times survive daylight saving — once a day, every day

Category: **correctness / cross-platform scheduling** (the last rounds were search UI, disk usage
and run-page features). Idea "DST gap / repeated-hour warning in the cron preview" (10-26 20:00,
10-27 00:05) turned out to hide two real bugs in robfig/cron, which the scheduler, the preview,
Home's "next 24 hours" and the budget estimate all used directly:

- clocks forward: "30 2 * * *" in America/New_York had no run at all on 2026-03-08;
- clocks back: "0 1 * * *" ran twice on 2026-11-01 (01:00 EDT and 01:00 EST) — twice the cost;
- zones with a 30-minute shift (Australia/Lord_Howe): robfig skipped whole days for *every*
  daily time around the change (its DST handling assumes one-hour steps).

Asia/Shanghai has no DST, so nothing changes there; it matters as soon as the scheduling
timezone (Settings › General) is a DST zone, on any OS (Windows uses the embedded tzdata).

**Done**

- `internal/scheduler/dst.go`: `NextIn(sched, from, loc)` matches cron fields against the wall
  clock (robfig walks a UTC copy, which has no DST) and resolves each matching wall time to real
  instants. Rules as in croner: a **fixed-time** job (one minute and one hour) whose time doesn't
  exist runs once when the clock has jumped (03:00), if still the same day; a time that occurs
  twice runs only on the first pass. **Interval** jobs (`*/15`, hourly, `9-17`) skip times in
  the gap and run on both passes of the repeated hour, so the spacing stays even (identical to
  robfig in whole-hour zones; a second walk from the fall-back edge finds the later pass).
  `@every` and `CRON_TZ=` schedules are left to robfig.
- `Scheduler.NextRun` (due runs, missed-run catch-up, `Realign`) goes through it, so stored next
  runs that the old code put on a duplicate or skipped day are corrected at start-up by the
  existing realign. Home's upcoming list and the budget "runs per day" estimate use it too (the
  10-27 idea "route through Sched.NextRun").
- `NextDSTNote`: the preview answer carries `dst` {kind gap / skip / fold, date, wall, at} for
  the first change within a year. The schedule editor shows one quiet amber line under the
  next runs, e.g. "夏令时：3/14周日 没有 02:30（时钟拨快），当天改在 03:00 运行" or "11/1周日 的
  01:00 会出现两次（时钟拨慢），只运行一次"; the rule and the zone are in the tooltip. Task detail's
  next-run tooltip adds the same sentence. zh-CN and en.
- Tests: `TestNextInDST` (gap, weekly gap, fold, created on either pass, hourly both passes and
  gap, interval in gap, Lord Howe ±30 min), `TestNextInOncePerDay` (9 fixed times × 5 zones ×
  2 years: exactly one run per calendar day; interval schedules equal robfig over 3000 steps),
  `TestNextDSTNote`, `TestSchedulerNextRunDST` (UTC from, as read from the database),
  `TestSchedulerPreviewDSTNote`, `TestRunsPerDayDST`, `TestSchedulerDSTUI`.
- Shots: `/workspace/tidebot-shots/2026-10-28-schedule-dst-1024x655.png` (gap, tooltip open),
  `…-schedule-dst-fold-1024x655.png` (fold; the list shows 11/1 01:00 once), and `-en`.

**Inspiration**

- Hexagon/croner `src/croner.ts` (`_findOccurrence` / `_walk` / `_runAt`: walk the wall clock,
  resolve each match as single / ambiguous / gap; fixed-time jobs run the first pass of a repeat
  and at the gap end if on the same day, interval jobs both passes and skip the gap; a second walk
  from the fall-back edge when the reference is on the first pass) and `src/pattern.ts`
  (`isFixedTime`). Cross-checked with cuu508/cronsim (healthchecks' cron library,
  `cronsim/cronsim.py`, `fixup_tz` handling). Ideas only, no code copied.

**Learned**

- robfig/cron's DST handling only works for one-hour shifts; walking the wall clock in UTC and
  resolving instants afterwards is simpler and correct for any offset change.
- Go's `time.Date` picks one side for a missing or doubled local time without saying which;
  `Time.ZoneBounds` (Go 1.19+) gives the transition instants needed to tell them apart.
- "Fixed time" must be decided per expression: an hourly job that ran twice in the repeated hour
  is right, a daily one is a bug.

**Ideas for later**

1. Per-task timezone (n8n has one per workflow), with the note in that zone.
2. Mark the adjusted time in the preview list itself (small "夏令时" tag on that row).
3. A notification the day before a change for tasks whose time moves ("明天 02:30 的任务改在 03:00").
4. Show the next change for the scheduling timezone in Settings › General's timezone tooltip.

## 2026-10-29 (00:05): task list, Home and spend page stay fast with a long run history

Category: **performance** (the last rounds were DST correctness, search UX, log compression,
prompt correctness and features; the last performance round was 10-26 08:40). Measured on the
demo server with 50 000 runs and ledger rows (5 tasks): `GET /api/tasks` and `/api/dashboard`
took ~450 ms each, `/api/runs/counts` 47 ms, `/api/spend/breakdown?range=all` 300 ms — the
task list and Home re-poll these, and the spend page loads `/api/tasks` first. A task every
15 minutes reaches 35 000 runs in a year.

**Done**

- Last N runs per task (task list strip, Home heartbeat bars) — `recentBeats`: each task reads its
  newest N entries of `idx_runs_task_created` (`r.rowid IN (SELECT rowid … WHERE task_id = t.id
  ORDER BY created_at DESC, id DESC LIMIT ?)`) instead of `ROW_NUMBER() OVER (PARTITION BY
  task_id)` over `SELECT r.*`, which numbered and copied every run ever made (prompt and final
  answer included) to keep 10. Same rows and order (ties by id); Home's bars now also break ties
  by id. `/api/tasks` 450 → 1.4 ms, `/api/dashboard` 480 → 2.4 ms.
- `/api/runs/counts` joins tasks only when there is search text (the only use of the task name):
  the count comes from `idx_runs_status` alone, 47 → 4 ms.
- `/api/spend/breakdown`: sums the ledger first, then looks up the ≤ 50 task names; runs are
  joined only for model / provider keys (older ledger rows without them); "all" no longer walks
  the day index; the chart series sums only the last 90 days (all it can show). 300 → 170 ms.
- Tests: `TestRecentBeatsMatchesWindow` (1 500 runs in random order with equal timestamps: same
  result as the old window query for 1 / 10 / 20 / 400, plan uses the index and never scans
  runs), `TestTaskListAndDashboardHistory`, `TestRunCountsHistory` (no-search counts = table,
  task-name search still matches), `TestSpendBreakdownHistory` (every range × key equal to the
  old one-pass query; series within 90 days).
- No UI change, no new strings, no migration.

**Inspiration**

- louislam/uptime-kuma `server/client.js` (`sendHeartbeatList`: per monitor `ORDER BY time DESC
  LIMIT 100` on the `(monitor_id, time)` index from `db/old_migrations/patch-improve-performance.sql`;
  `sendHeartbeatLists`: narrow column list, join on the picked ids instead of whole rows). Ideas
  only, no code copied.

**Learned**

- In SQLite a window function over `SELECT *` materialises every column of every row before the
  outer `rn <= N`; the cost grew with the size of prompts and answers, not just the row count.
- `rowid IN (correlated LIMIT subquery)` gives a per-group top-N that stops early on an index —
  the rowid is part of every index entry, so the inner query never touches the table.
- modernc.org/sqlite is ~2–3× slower than the C library on the same plan; measure through the
  server, not only with the Python sqlite3 module.

**Ideas for later**

1. Global search (`/api/search`, ~140 ms at 50 000 runs) and `/api/runs/counts?q=` (~130 ms) scan
   every prompt: an FTS5 table over prompt / error (trigram tokenizer for Chinese) would make both
   index lookups.
2. Keep a per-day rollup of the ledger (like Uptime Kuma's `stat_daily`) so "all time" spend is a
   sum of a few hundred rows.
3. A `scripts/bench-history.sh` that seeds N runs into the demo server and prints endpoint
   timings, run before each weekly release to catch regressions.
4. Retention's "keep the last N runs per task" (`internal/app/system.go`) still uses a window
   query; it only selects ids, but could use the same per-task index read.

## 2026-10-29 (04:00): tasks carry tags — the list filters and searches by them

Category: **feature / UX** (the last rounds were performance, DST correctness, search UX, log
compression and prompt correctness). With a dozen tasks the list had only status and skill to
narrow it; nothing grouped "daily reports" or "client A" together.

**Done**

- Migration `017_task_tags.sql`: `task_tags(task_id, tag, pos)`, primary key per task and tag,
  index on tag. Archived tasks keep theirs but are never listed or suggested.
- `internal/webui/task_tags.go`: tags are trimmed, inner spaces folded, a leading `#`/`＃`
  dropped, duplicates removed without case (first spelling wins), max 8 × 24 characters, no
  commas or control characters. A tag typed in another case joins the spelling already in use
  ("daily" → "Daily"). Parameterized SQL, one transaction per save.
- API: the task form carries `tags` (left out = unchanged, so older pages and agentctl don't
  wipe them; `[]` clears); `/api/tasks`, the detail and `/form` answer with them (list: one
  query for all tasks); `GET /api/tasks/tags` → tags in use with counts; batch actions
  `add_tag` / `remove_tag`; duplicate copies tags; export / import carry them.
- WebUI: chip input in the task form under the prompt (Enter, comma or Tab adds, Backspace on an
  empty box removes the last, × removes one, IME composition left alone, Enter never submits),
  tags already in use offered underneath with their counts in the tooltip. Task list: up to two
  chips beside the name (+N with the rest in the tooltip); clicking one filters, a "全部标签"
  menu (only when tags exist) does the same, the address keeps `?tag=`; the search also matches
  tag names; the batch bar has "标签" (add to / remove from the selected tasks, in-app dialog).
  Task page: chips after the created/updated line, each links to the filtered list. Hue from an
  FNV-1a hash of the lowercased name into six quiet sand-theme colours — same on every page and
  OS, no colour picker. Toolbar search box now shrinks (140–240 px) so search, status chips,
  skill and tag menus stay on one row at 1024 px in both languages. zh-CN and en.
- Tests: `TestNormalizeTags`, `TestTaskTagsAPI` (create / keep / clear, case joins, list, detail,
  form, in-use counts, batch add / remove / empty, duplicate, export → import),
  `TestTaskTagsUI` (wiring, strings, no native dialogs, escaping, hue spread via node).
- Shots: `/workspace/tidebot-shots/2026-10-29-task-tags-list-1024x655.png`, `…-filter-…`,
  `…-form-…` and the `-en` variants.

**Inspiration**

- louislam/uptime-kuma `src/components/TagsManager.vue` (tags picked from the ones in use, a
  name in another case is "already exists"), `src/components/Tag.vue` (small chip, optional ×),
  `src/components/MonitorList.vue` (search text also matches tag names; filter by tags). Ideas
  only, no code copied; tidebot drops colours and values to keep it one field.

**Learned**

- A string hash with multiplier 31 is useless modulo 6 (31 ≡ 1), the "hue" became the sum of the
  character codes and most CJK pairs landed on the same colour; FNV-1a spreads them.
- Optional pointer fields (`*[]string`) keep "left out" and "cleared" apart, which matters for
  agentctl and older open pages that PUT the form without the new field.

**Ideas for later**

1. Filter runs and spend by tag (spend breakdown "by tag").
2. Several tags in the filter at once (Uptime Kuma allows any-of).
3. Rename / merge a tag everywhere from a small "manage tags" list in Settings.
4. `agentctl tasks create --tag` and tags in the global ⌘K search.

## 2026-10-29 (08:00): shortcuts speak each OS — ⌘K on a Mac, Ctrl+K elsewhere, and only that

Category: **cross-platform / keyboard** (the last rounds were tags (feature / UX), list
performance and DST correctness; the last cross-platform round was 10-28 20:00). Three
differences showed up across macOS, Windows and Linux: the inline editor said "Ctrl+Enter 保存"
on a Mac, quick run's tooltip read "⌘+Enter", sort hints said "Alt" where a Mac keyboard says
⌥; the palette also opened on Ctrl+K on a Mac (that is "delete to end of line" in every macOS
text field) and on Ctrl+Alt+K on Windows (AltGr+K on Polish / German layouts); and the
single-letter shortcuts (j / k, g h, n t, r / e / o, x) did nothing with Caps Lock on or a
Russian / Greek layout.

**Done**

- `app.js` key helpers next to the IME guard: `KB_MAC` (one platform check, `userAgentData`
  then `navigator.platform` then the UA; replaces three copies of the `navigator.platform`
  regex), `keyLabel("Mod+Enter")` → "⌘Enter" on a Mac / "Ctrl+Enter" elsewhere (⌘ ⌥ ⇧ ⌃
  written together on a Mac, `Ctrl+` `Alt+` `Shift+` elsewhere; `aria` gives Command / Option /
  Control for screen readers), `kbdHtml()` (one `<kbd>` per key of a sequence, "g h" → two),
  `modKey(e)` (⌘ alone on a Mac, Ctrl alone elsewhere, never with Alt), `submitKey(e)`
  (⌘ or Ctrl + Enter on every OS — neither gives it a meaning in a text box, and earlier hints
  taught Ctrl+Enter), `kbLetter(e)` (letters by meaning: Caps Lock lowercased, Shift kept,
  a non-Latin character falls back to `e.code` KeyJ → j).
- Topbar search badge, shortcuts dialog (new row "⌘Enter / Ctrl+Enter: save or run from a
  text box"; reorder shown as ⌥← / ⌥→), quick run tooltip, inline editor hint, Buttons and
  providers sort hints now go through `keyLabel`. The four strings take `{k}` in zh-CN and en;
  zh-CN also gets full-width punctuation there.
- Palette: `modKey` + `kbLetter` (⌘K on a Russian layout still opens it). Global and list
  keys use `kbLetter`.
- Tests: `TestPlatformKeys` (wiring; no `navigator.platform` outside `KB_MAC`, no hard-coded
  "⌘ K" / "Ctrl K", no modifier names in either dictionary; node: labels Mac / PC / ARIA,
  chords, sequences; modKey on both OSes incl. AltGr and the Windows key; submitKey; kbLetter
  for Caps Lock, Shift, Russian, Greek, French digit row, punctuation). `TestListKeyboardCursor`
  and `TestQuickRunMemory` follow the helpers.
- Shots (macOS UA zh-CN / Windows UA en): `/workspace/tidebot-shots/2026-10-29-shortcuts-mac-zh-1024x655.png`,
  `…-shortcuts-win-en-…`, `…-shortcuts-inline-mac-zh-…`, `…-shortcuts-inline-win-en-…`.

**Inspiration**

- microsoft/vscode `src/vs/base/common/keybindingLabels.ts` (`UILabelProvider`: per-OS
  modifier names and separator — ⌘⌥⇧⌃ with no separator on macOS, "Ctrl+" elsewhere;
  `AriaLabelProvider`: long names "Command", "Option" for screen readers).
- n8n-io/n8n `packages/frontend/@n8n/composables/src/useDeviceSupport.ts` (`isCtrlKeyPressed`:
  metaKey on macOS, ctrlKey elsewhere; `controlKeyText`) and
  `packages/frontend/@n8n/design-system/src/components/N8nKeyboardShortcut/N8nKeyboardShortcut.vue`
  (one key cap per key, ⌥ vs Alt). Ideas only, no code copied.

**Learned**

- "metaKey || ctrlKey" is the common shortcut check and wrong on both sides: it steals
  Control-based text editing on macOS and fires on AltGr (reported as Ctrl+Alt) on Windows.
- `e.key` follows the layout and Caps Lock; `e.code` names the physical key. Shortcuts should
  prefer the meaning and fall back to the position only when the layout has no Latin letter.

**Ideas for later**

1. Show a key hint in more button tooltips ("立即运行 · r", "编辑 · e") with `kbdHtml`, like
   VS Code's "(⌘K)" suffix in hover titles.
2. ⌘/Ctrl+Enter submits every dialog form (task form, provider drawer, AI plugin), with the hint
   on the primary button.
3. ⌘/Ctrl+S in the skill file editor and the task form.
4. Desktop app menu accelerators (macOS menu bar: ⌘, opens Settings, ⌘N new task) wired to the
   same commands.

## 2026-10-29 (12:00): tidebot unreachable — one translated message and a bar that reconnects

Category: **robustness / cross-platform** (the last rounds were keyboard (cross-platform), tags
(feature), list performance and DST correctness). When agentd restarted, was upgraded, or the
laptop woke up with the server-mode page open, each engine reported it its own way and in English
— "Failed to fetch" (Chrome / WebView2), "Load failed" (Safari / WKWebView), "NetworkError when
attempting to fetch resource." (Firefox) — as a red toast per click or an error page, live pages
kept polling, nothing said when it was back, and a failed start fell through to the login page.

**Done**

- `app.js` connection block next to `fail()`: `netFetch()` wraps every fetch to tidebot's own
  endpoints (`apiRaw`, downloads, desktop save, uploads); no answer (TypeError) or a reverse proxy's
  own 502 / 503 / 504 page (not JSON) becomes `err.offline` with `e.offline = true`; our own
  timeout (AbortError) stays a timeout; any answer from tidebot clears the state.
- A short blip shows nothing: the first probe (`GET /api/rev`, 8 s timeout, 401 counts as "back")
  goes out after 1.5 s. If it fails, a pill at the bottom centre says "与 tidebot 的连接已断开 ·
  N 秒后重试" with "立即重试"; retries back off 2, 3, 5, 5, 10, 10, 15, then every 30 s. Disconnect
  time and attempt count are in the tooltip. The bar is built once per outage and only the
  countdown text changes (the button keeps focus, `role=status` announces once, the countdown is
  `aria-hidden`). Fixed position, so the page never shifts; toasts move up while it shows; above
  dialogs and drawers, under tooltips; no animation with reduced motion.
- Hidden tab: the countdown stands still (no requests); coming back, the `online` event and a
  dropped live-log stream (`EventSource` error) all trigger a check. Live pages skip their
  `/api/rev` poll while down.
- Back: "已重新连接" for 2 s, the offline toast closes, the sidebar Claude Code status and the
  bell reload, an error page is routed again, any other page repaints in place — unless a dialog
  is open or someone is typing on the page. A start that failed for lack of connection keeps the
  splash and boots again instead of showing the login page.
- `fail()`: an offline error toasts once, not once per click.
- zh-CN and en (`err.offline`, `conn.*`; the `net.*` keys already belong to the proxy settings).
  Lucide `wifi-off` icon added as `offline`.
- Tests: `TestConnectionBar` (wiring; no raw `fetch()` outside `netFetch`, the probe and the i18n
  loader; CSS; keys and placeholders in both languages; node with a fake clock, DOM and fetch:
  Chrome / Safari / Firefox messages, AbortError, proxy 502/503 vs JSON 503, 500, 401, backoff
  list, blip shows nothing and repaints nothing, bar after a failed probe, countdown, bar built
  once, hidden tab makes no probes, one offline toast, "Retry now", reconnect repaints and hides
  the bar after 2 s, dialog keeps the page, error page re-routed, failed start boots again).
- Shots (real agentd stopped and started again):
  `/workspace/tidebot-shots/2026-10-29-connection-lost-1024x655.png`, `…-connection-back-…` and
  the `-en` variants.

**Inspiration**

- louislam/uptime-kuma `src/mixins/socket.js` (2 s delay before "Lost connection to the socket
  server. Reconnecting…" to avoid noise; `connectCount` to reload data after a reconnect) and
  `src/layouts/Layout.vue` (one `lost-connection` bar instead of per-request errors).
- microsoft/vscode `src/vs/workbench/contrib/remote/browser/remote.ts` ("Attempting to reconnect
  in N seconds…", "Reconnect Now" skips the wait) and
  `src/vs/platform/remote/common/remoteAgentConnection.ts` (growing wait list
  `[0, 5, 5, 10, 10, 10, 10, 10, 30]`). Ideas only, no code copied.

**Learned**

- A fetch that gets no answer rejects with a TypeError whose message differs per engine; the
  type is the only portable signal, and an aborted fetch (our timeout) must be kept apart by name.
- Rebuilding a live region every second makes it unclickable (the button is replaced mid-click)
  and noisy for screen readers; update one text node instead.

**Ideas for later**

1. Show the version agentd came back with, and offer a reload when the static assets changed
   (an upgrade under an open tab).
2. Queue a failed "run now" while offline and offer to send it on reconnect.
3. Desktop app: when the embedded server stops, show the same bar instead of a blank window.

## 2026-10-29 (16:00): success notices only when the answer changed — digest and watch tasks stop repeating themselves

Category: **feature / notifications** (the last rounds were connection robustness, keyboard
(cross-platform), tags, list performance and DST correctness). A daily digest or a "has this page
changed?" task with "notify on success" sent the same message every day even when nothing was
new, so people turned success notices off and missed the days that mattered.

**Done**

- Migration `018_answer_same.sql`: `task_policies.notify_changed` (off by default) and
  `runs.answer_same_as` (id of the earlier run with the same answer).
- `db.AnswerKey` folds every run of whitespace (spaces, tabs, blank lines, CRLF, full-width
  space) into one space; `Store.MarkAnswerSame` compares a succeeded run's final answer with the
  task's previous succeeded run that has one (indexed by `task_id, created_at`, parameterized SQL)
  and stores the match. Always compared, so switching the option on later already knows the last
  answer; failed runs in between don't count; empty answers and the first run never match.
- `app`: after a success, `markAnswerSame` sets `Event.AnswerSameAs` and `Event.Unchanged` (the
  task wants only changes); `EventToMessage` drops the notice for every channel and the bell, the
  desktop shell skips its banner. Failure, approval and budget notices are never held back.
- Task form › 失败与补跑: a third line under 成功时也通知 — 仅在回答有变化时通知, tooltip with the
  rule; the two sub-options grey out (class, not `:has()`, for older WebKitGTK) while success
  notices are off; the folded summary adds 回答有变化才通知. `policy.set`, older pages, imported
  files and agentctl that don't send the field keep the task's choice; export carries it.
- Run page: the final answer card gets a quiet chip 与上次相同 linking to the matching run; the time
  of that run and the whitespace rule are in its tooltip. A run cleaned up since shows no chip.
- zh-CN and en strings. Tests: `TestSuccessNoticeOnlyWhenAnswerChanged`,
  `TestPolicySetKeepsNotifyChanged`, `TestTaskFormNotifyChanged`, `TestRunAnswerSameAPI`,
  `TestNotifyChangedUI`, `TestAnswerKey`; new-task defaults test updated.
- Shots: `/workspace/tidebot-shots/2026-10-29-1600.png` (form, tooltip open) and
  `…-1600-run.png` (chip with tooltip), real agentd with mock Claude at 1024×655.

**Inspiration**

- dgtlmoon/changedetection.io `changedetectionio/processors/text_json_diff/processor.py`:
  `ChecksumCalculator.calculate(text, ignore_whitespace)` and the `previous_md5` comparison — a
  change is "the normalized text's checksum differs from last time", whitespace optionally
  ignored, and only a change triggers a notification. Idea only; we compare the normalized text
  directly (answers are capped at 128 KB) and keep the match on the run for the UI.

**Learned**

- Keeping the comparison result on the run (not only deciding the notice) costs one indexed
  query per success and makes the decision visible afterwards — "why didn't I get a message?" is
  answered on the run page.
- Agents re-wrap the same content; comparing raw text would call almost every run a change.

**Ideas for later**

1. Ignore lines matching a pattern (dates, "generated at …") before comparing, like
   changedetection.io's `ignore_text`.
2. A diff of this answer against the previous one on the run page (line diff we already have for
   skills).
3. Mark runs with an unchanged answer in the task's last-10 strip (hollow dot) and the runs list.
4. "Notify when the answer changed" for failures too: send a failure once, not every retry day.
