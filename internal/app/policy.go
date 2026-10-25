package app

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// EventBreakerTripped: a task was paused after consecutive failures.
const EventBreakerTripped = "breaker_tripped"

var taskLocks sync.Map // task id → *sync.Mutex; serializes catch-up runs of one task

// runTaskScheduled is the scheduler's RunnerFunc: it returns immediately and runs the task
// in the background with retry + circuit breaker (final-plan §12).
func (a *App) runTaskScheduled(ctx context.Context, task db.Task) error {
	go a.runWithPolicy(context.Background(), task, "cron", cost.BucketProduction, 0)
	return nil
}

func (a *App) runWithPolicy(ctx context.Context, task db.Task, trigger, bucket string, attempt int) {
	mu, _ := taskLocks.LoadOrStore(task.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	err := a.runTaskTrigger(ctx, task, trigger, bucket)
	mu.(*sync.Mutex).Unlock()
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
		time.AfterFunc(a.retryDelay(wait), func() { a.runWithPolicy(context.Background(), task, "retry", bucket, attempt+1) })
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
