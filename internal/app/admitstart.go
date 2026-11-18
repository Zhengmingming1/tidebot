package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// startReservation is the budget/quota hold taken at actual launch.
type startReservation struct {
	hold          *cost.Hold
	accountingAt  time.Time
	accountingDay string
	launchedID    string
}

func runStartAborted(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	if why := context.Cause(ctx); why != nil {
		return why
	}
	return ctx.Err()
}

func retryableStartErr(err error) bool {
	if err == nil {
		return false
	}
	// Permanent refusals must not be retried, even if the message mentions a
	// locked database (a pending ledger write often failed with SQLITE_BUSY).
	if errors.Is(err, errAccountingPending) || errors.Is(err, errBudgetHeld) || errors.Is(err, errTaskInactive) {
		return false
	}
	if errors.Is(err, runner.ErrStoppedByUser) || errors.Is(err, runner.ErrShuttingDown) || errors.Is(err, runner.ErrSkipped) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	s := err.Error()
	if strings.Contains(s, "budget blocked") {
		return false
	}
	return strings.Contains(s, "database is locked") || strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "SQLITE_LOCKED")
}

// retryStartOp retries a launch-time database step while the database is busy.
// The step uses its own short timeout, not runCtx: a canceled or briefly busy
// call must not skip a run that can still start. runCtx is still watched so a
// user stop or shutdown aborts the launch.
func (a *App) retryStartOp(runCtx context.Context, what, id string, fn func(context.Context) error) error {
	if runCtx == nil {
		runCtx = context.Background()
	}
	end := time.Now().Add(writeRetryFor)
	pause := writeRetryPause
	for {
		if err := runStartAborted(runCtx); err != nil {
			return err
		}
		ctx, done := context.WithTimeout(context.Background(), writeRetryWait)
		err := fn(ctx)
		done()
		if err == nil {
			if stop := runStartAborted(runCtx); stop != nil {
				return stop
			}
			return nil
		}
		if !retryableStartErr(err) {
			return err
		}
		if a.policyRunsClosing() || time.Now().Add(pause).After(end) {
			a.Log.Warn(what+": gave up", "id", id, "error", err)
			return err
		}
		a.Log.Warn(what+": retrying", "id", id, "error", err)
		timer := time.NewTimer(pause)
		select {
		case <-runCtx.Done():
			timer.Stop()
			return runStartAborted(runCtx)
		case <-timer.C:
		}
		if pause < 8*writeRetryPause {
			pause *= 2
		}
	}
}

// admitRunRetry is admitRun with the same short-timeout retry used at launch.
// RunPromptEx's first check happens before a run row exists, so a busy database
// or a canceled caller context used to fail the request before it was even
// queued. Permanent budget or quota refusals are not retried. A user stop or
// shutdown on ctx still aborts immediately.
func (a *App) admitRunRetry(ctx context.Context, what, id, taskID, bucket string, prof provider.Profile, callerCap float64) (float64, *runner.CostControl, error) {
	var runMax float64
	var cc *runner.CostControl
	err := a.retryStartOp(ctx, what, id, func(wctx context.Context) error {
		var err error
		runMax, cc, err = a.admitRun(wctx, taskID, bucket, prof, callerCap)
		return err
	})
	return runMax, cc, err
}

// admitAtStart checks budgets and reserves them at actual launch. It must not
// use runCtx for the database: AtStart is given that context, and a canceled
// or briefly busy call used to skip a run that could have started. A user
// stop or shutdown on runCtx still aborts before the run starts.
func (a *App) admitAtStart(runCtx context.Context, o *runner.Options, bucket string, prof provider.Profile, callerCap float64) (startReservation, error) {
	var res startReservation
	err := a.retryStartOp(runCtx, "admit at start", o.RunID, func(wctx context.Context) error {
		return a.admitAtStartOnce(wctx, o, bucket, prof, callerCap, &res)
	})
	if err != nil {
		a.releaseStartReservation(&res)
		return startReservation{}, err
	}
	return res, nil
}

func (a *App) admitAtStartOnce(wctx context.Context, o *runner.Options, bucket string, prof provider.Profile, callerCap float64, res *startReservation) error {
	policyMu := db.CostPolicyMutex(a.Store.DB)
	policyMu.Lock()
	defer policyMu.Unlock()
	// A previous busy attempt may have held budget or quota; drop it before
	// checking again so a retry cannot reserve twice.
	a.releaseStartReservationLocked(res)
	rm, ncc, err := a.admitRun(wctx, o.TaskID, bucket, prof, callerCap)
	if err != nil {
		return err
	}
	o.Cost = ncc
	o.ExtraArgs = a.budgetArgs(o.ExtraArgs, prof, rm)
	h, err := a.holdRunBudget(wctx, o, bucket, prof)
	if err != nil {
		return err
	}
	res.hold = h
	res.accountingAt = time.Now()
	if h != nil {
		res.accountingAt = h.AccountingAt()
	}
	res.accountingDay = res.accountingAt.In(a.Loc()).Format("2006-01-02")
	res.launchedID = o.RunID
	return a.reserveProviderQuota(wctx, o, prof)
}

func (a *App) releaseStartReservation(res *startReservation) {
	if res == nil {
		return
	}
	policyMu := db.CostPolicyMutex(a.Store.DB)
	policyMu.Lock()
	defer policyMu.Unlock()
	a.releaseStartReservationLocked(res)
}

func (a *App) releaseStartReservationLocked(res *startReservation) {
	if res == nil {
		return
	}
	res.hold.Release()
	res.hold = nil
	if res.launchedID != "" {
		delete(a.quotaHolds, res.launchedID)
		res.launchedID = ""
	}
}
