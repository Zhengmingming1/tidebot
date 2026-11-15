package app

import (
	"context"
	"time"
)

// Bookkeeping writes share tidebot's one database connection with everything
// else; a long cleanup or export can hold it past one try.
var (
	writeRetryWait  = 3 * time.Second // one try
	writeRetryPause = time.Second     // first pause between tries (doubles, up to 8x)
	writeRetryFor   = 2 * time.Minute // give up after
)

// retryWrite runs a bookkeeping write, retrying while the database is busy.
// Every write passed in is safe to repeat. id is only for the log.
func (a *App) retryWrite(what, id string, fn func(context.Context) error) error {
	return a.retryWriteKey(what, "id", id, fn)
}

func (a *App) retryWriteKey(what, key, id string, fn func(context.Context) error) error {
	end := time.Now().Add(writeRetryFor)
	pause := writeRetryPause
	for {
		ctx, done := context.WithTimeout(context.Background(), writeRetryWait)
		err := fn(ctx)
		done()
		if err == nil {
			return nil
		}
		if a.policyRunsClosing() || time.Now().Add(pause).After(end) {
			a.Log.Warn(what+": gave up", key, id, "error", err)
			return err
		}
		a.Log.Warn(what+": retrying", key, id, "error", err)
		time.Sleep(pause)
		if pause < 8*writeRetryPause {
			pause *= 2
		}
	}
}
