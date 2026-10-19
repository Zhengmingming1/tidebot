package app

import (
	"context"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// maxInterruptedNotices caps how many "run interrupted" notices one start sends.
const maxInterruptedNotices = 5

// recoverInterruptedRuns runs once per start, after this process owns the IPC
// listener and before the scheduler starts. It closes runs the previous core
// left queued/running and tells the user which ones did not finish. The idea
// follows n8n's startup recovery (unfinished executions marked "crashed" and
// announced per execution); tidebot reuses its "canceled" status.
func (a *App) recoverInterruptedRuns(ctx context.Context) {
	if a.Store == nil {
		return
	}
	runs, err := a.Store.RecoverInterruptedRuns(ctx, time.Now())
	if err != nil {
		a.Log.Error("recover interrupted runs", "error", err)
	}
	if len(runs) == 0 {
		return
	}
	a.Log.Warn("closed runs left over by the previous core", "count", len(runs))
	if a.Notify == nil {
		return
	}
	go func() {
		quiet, _ := a.settingString(ctx, "notify.quiet")
		for i, r := range runs {
			if i == maxInterruptedNotices {
				break
			}
			e := Event{Kind: EventRunFailed, TaskID: r.TaskID, TaskName: a.taskName(r.TaskID), RunID: r.ID, Detail: db.RunInterruptedMsg}
			if m, ok := a.EventToMessage(e); ok {
				if _, err := a.Notify.Dispatch(ctx, m, quiet); err != nil {
					a.Log.Warn("notify", "error", err)
				}
			}
		}
	}()
}
