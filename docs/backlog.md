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
