package app

import (
	"testing"
	"time"
)

// shortWriteRetries shrinks the retry pauses of bookkeeping writes.
func shortWriteRetries(t *testing.T) {
	t.Helper()
	wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
	writeRetryWait, writeRetryPause, writeRetryFor = time.Second, 10*time.Millisecond, 20*time.Second
	t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
}
