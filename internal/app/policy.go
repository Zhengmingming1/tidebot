package app

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// EventBreakerTripped: a task was paused after consecutive failures.
const EventBreakerTripped = "breaker_tripped"

// runTaskScheduled is the scheduler's RunnerFunc: it returns immediately and runs the task
// in the background with retry + circuit breaker (final-plan §12).
func (a *App) runTaskScheduled(ctx context.Context, task db.Task) error {
	go a.runWithPolicy(context.Background(), task, "cron", cost.BucketProduction, 0)
	return nil
}

func (a *App) runWithPolicy(ctx context.Context, task db.Task, trigger, bucket string, attempt int) {
	release, ok := a.enterPolicyRun(task)
	if !ok {
		return
	}
	defer release()
	if a.policyRunsClosing() || ctx.Err() != nil {
		return
	}
	err := a.runTaskTrigger(ctx, task, trigger, bucket)
	if waitsOut(err) {
		// Another run is changing the task's folder (a session turn, a Run dialog
		// run, a workflow), or a finished run's cost is still being written to the
		// ledger: not the task's fault and not a failed run. Wait until it clears
		// and go then; no retry used, no breaker strike. Later firings of the task
		// meanwhile are dropped (enterPolicyRun), so one wait ends in one run.
		a.Log.Info("task waits before running", "task_id", task.ID, "why", err.Error())
		defer a.markPolicyWaiting(task)()
		for waitsOut(err) {
			for {
				if !a.sleepPolicy(ctx, dirBusyPoll) || !a.taskStillRunnable(ctx, task.ID, trigger) {
					return // closing, or the task was paused / deleted meanwhile
				}
				if a.accountingFailure.Load() == nil {
					break // the ledger is written (a busy folder is checked by trying)
				}
			}
			err = a.runTaskTrigger(ctx, task, trigger, bucket)
		}
	}
	if a.policyRunsClosing() || ctx.Err() != nil {
		return
	}
	if errors.Is(err, runner.ErrShuttingDown) || errors.Is(err, runner.ErrStoppedByUser) || errors.Is(err, errTaskInactive) || errors.Is(err, errDiskFull) || errors.Is(err, errBudgetHeld) || errors.Is(err, errPowerSaving) {
		// quitting, a user stop, a task paused while its run waited, a full disk, a
		// budget held by runs already going or saving battery is not the task's fault: no retry, no breaker strike
		return
	}
	if a.Policy == nil {
		return
	}
	p := a.Policy.Get(ctx, task.ID)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	class, retryable := policy.Classify(errMsg)
	if err != nil && retryable && attempt < p.RetryMax {
		wait := policy.Backoff(attempt+1, rand.New(rand.NewSource(time.Now().UnixNano())))
		a.Log.Info("retry scheduled", "task_id", task.ID, "class", class, "attempt", attempt+1, "in", wait.String())
		a.schedulePolicyRetry(ctx, a.retryDelay(wait), func() { a.runWithPolicy(ctx, task, "retry", bucket, attempt+1) })
		return
	}
	_, tripped, _ := a.Policy.RecordOutcome(ctx, task.ID, err == nil)
	if tripped {
		if a.Tasks != nil {
			_ = a.Tasks.Pause(ctx, task.ID, tasksvc.PauseBreaker, class)
		}
		a.Events.Emit(Event{Kind: EventBreakerTripped, TaskID: task.ID, TaskName: task.Name, Detail: class})
	}
}

// waitsOut: the run could not start for a reason that clears by itself.
func waitsOut(err error) bool {
	return errors.Is(err, guard.ErrDirBusy) || errors.Is(err, errAccountingPending)
}

// dirBusyPoll: how often a scheduled run whose folder is busy looks again (a var for tests).
var dirBusyPoll = 3 * time.Second

// sleepPolicy waits d unless tidebot starts closing or ctx ends (false then).
func (a *App) sleepPolicy(ctx context.Context, d time.Duration) bool {
	t := time.NewTicker(dirBusyPollStep(d))
	defer t.Stop()
	end := time.Now().Add(d)
	for {
		if a.policyRunsClosing() || ctx.Err() != nil {
			return false
		}
		if !time.Now().Before(end) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

func dirBusyPollStep(d time.Duration) time.Duration {
	if d > 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	if d <= 0 {
		return time.Millisecond
	}
	return d
}

// retryDelay allows tests to shrink backoff (TIDEBOT_RETRY_SCALE=0.001).
func (a *App) retryDelay(d time.Duration) time.Duration {
	if a.retryScale > 0 {
		return time.Duration(float64(d) * a.retryScale)
	}
	return d
}

func (a *App) catchup(task db.Task, missed []time.Time, now time.Time) int {
	p := a.Policy.Get(context.Background(), task.ID)
	return policy.CatchupRuns(p.Catchup, missed, now, time.Duration(p.CatchupGraceMinutes)*time.Minute)
}
