package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
)

// A run's final ledger write that failed (tidebot's one database connection
// busy past the try, a full disk) closes admission: no run starts without its
// accounting. It used to stay closed until tidebot restarted, so one busy
// moment refused every later run, session turn, task and workflow, even with no
// budget set. The entry is now kept and retried; when it lands the run's hold
// is released and admission opens again.

// ledgerRetryPause is the first pause between retries (it doubles up to 8×).
var ledgerRetryPause = time.Second

// ledgerWriteWait bounds one try of the write.
const ledgerWriteWait = 3 * time.Second

type pendingLedgerEntry struct {
	entry   cost.Entry
	hold    *cost.Hold // released once the entry is written
	quotaID string     // a.quotaHolds key, removed with the hold
}

func (a *App) costPolicyMu() *sync.Mutex { return db.CostPolicyMutex(a.Store.DB) }

// deferLedgerEntry keeps a run's entry whose final write failed and starts the
// retries. The caller holds the cost policy mutex.
func (a *App) deferLedgerEntry(e cost.Entry, h *cost.Hold, quotaID string, writeErr error) string {
	if a.pendingLedger == nil {
		a.pendingLedger = map[string]pendingLedgerEntry{}
	}
	a.pendingLedger[e.RunID] = pendingLedgerEntry{entry: e, hold: h, quotaID: quotaID}
	message := fmt.Sprintf("cost ledger persistence failed for %s: %v", e.RunID, writeErr)
	a.accountingFailure.Store(&message)
	if !a.ledgerRetrying && !a.ledgerQuit {
		a.ledgerRetrying = true
		a.ledgerStop = make(chan struct{})
		go a.retryLedger(ledgerRetryPause, a.ledgerStop)
	}
	return message
}

// flushPendingLedger writes the kept entries (the ledger upsert is idempotent)
// and reopens admission when none is left. The caller holds the cost policy
// mutex. It returns how many are still pending.
func (a *App) flushPendingLedger(wait time.Duration) int {
	for runID, p := range a.pendingLedger {
		ctx, done := context.WithTimeout(context.Background(), wait)
		err := a.Cost.RecordEntry(ctx, p.entry)
		done()
		if err != nil {
			a.Log.Warn("cost ledger retry failed", "run_id", runID, "error", err)
			continue
		}
		p.hold.Release()
		delete(a.quotaHolds, p.quotaID)
		delete(a.pendingLedger, runID)
		a.Log.Info("cost ledger written on retry", "run_id", runID)
	}
	if len(a.pendingLedger) == 0 {
		a.accountingFailure.Store(nil)
	}
	return len(a.pendingLedger)
}

func (a *App) retryLedger(first time.Duration, stop <-chan struct{}) {
	mu := a.costPolicyMu()
	pause := first
	for {
		select {
		case <-time.After(pause):
		case <-stop: // quitting: flushLedgerOnQuit makes the last try
			return
		}
		mu.Lock()
		if a.ledgerQuit {
			mu.Unlock()
			return
		}
		left := a.flushPendingLedger(ledgerWriteWait)
		if left == 0 || a.policyRunsClosing() {
			a.ledgerRetrying = false
			mu.Unlock()
			return
		}
		mu.Unlock()
		if pause < 8*first {
			pause *= 2
		}
	}
}

// flushLedgerOnQuit makes a last try at kept entries before the database closes.
func (a *App) flushLedgerOnQuit() {
	if a.Cost == nil || a.Store == nil {
		return
	}
	mu := a.costPolicyMu()
	mu.Lock()
	defer mu.Unlock()
	a.ledgerQuit = true
	if a.ledgerRetrying {
		a.ledgerRetrying = false
		close(a.ledgerStop)
	}
	if len(a.pendingLedger) == 0 {
		return
	}
	if left := a.flushPendingLedger(shutdownWriteWait); left > 0 {
		a.Log.Error("cost ledger entries lost at quit", "count", left)
	}
}
