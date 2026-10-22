package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

// launch answers with the run id as soon as the row exists, with the early error when the
// run never got a row (e.g. budget blocked), and with no id when the run is still queued.
func TestLaunch(t *testing.T) {
	logged := make(chan error, 4)
	onErr := func(err error) { logged <- err }

	release := make(chan struct{})
	id, err := launch(time.Second, func(onStart func(string)) error {
		onStart("run_a")
		<-release // still running when launch returns
		return nil
	}, onErr)
	close(release)
	if id != "run_a" || err != nil {
		t.Fatalf("started: %q %v", id, err)
	}

	blocked := errors.New("budget blocked: daily cap")
	if id, err := launch(time.Second, func(func(string)) error { return blocked }, onErr); id != "" || !errors.Is(err, blocked) {
		t.Fatalf("early error: %q %v", id, err)
	}

	// Started and failed right away: the run exists, so the id wins over the error.
	if id, err := launch(time.Second, func(onStart func(string)) error { onStart("run_b"); return errors.New("exit 1") }, onErr); id != "run_b" || err != nil {
		t.Fatalf("fast failure: %q %v", id, err)
	}

	queued := make(chan struct{})
	start := time.Now()
	id, err = launch(50*time.Millisecond, func(onStart func(string)) error { <-queued; onStart("late"); return nil }, onErr)
	close(queued)
	if id != "" || err != nil || time.Since(start) > time.Second {
		t.Fatalf("queued: %q %v after %v", id, err, time.Since(start))
	}
	for i := 0; i < 2; i++ { // both failures reach the log, even the one answered with an id
		select {
		case <-logged:
		case <-time.After(time.Second):
			t.Fatalf("only %d failures logged", i)
		}
	}
}

// Starting a task or a quick run from the WebUI returns the id of the run it created.
func TestStartReturnsRunID(t *testing.T) {
	t.Setenv("TIDEBOT_MOCK_STEPS", "3")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.2")
	a := newTestApp(t)
	ctx := context.Background()
	task, err := a.EnsureTask(ctx, "t", "hello", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.startTaskAsync(task.ID, "production")
	if err != nil || id == "" {
		t.Fatalf("task: %q %v", id, err)
	}
	runs, err := a.Store.ListRuns(ctx, task.ID, 5)
	if err != nil || len(runs) != 1 || runs[0].ID != id {
		t.Fatalf("task run %q not stored: %v %v", id, runs, err)
	}

	qid, err := a.startQuickRunAsync("quick hello", "")
	if err != nil || qid == "" || qid == id {
		t.Fatalf("quick: %q %v", qid, err)
	}
	var trig string
	if err := a.DB.QueryRowContext(ctx, `SELECT trigger_kind FROM runs WHERE id=?`, qid).Scan(&trig); err != nil || trig != "quick" {
		t.Fatalf("quick run row: %q %v", trig, err)
	}
	if _, err := a.startQuickRunAsync("x", "no-such-provider"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	waitFor(t, 15*time.Second, func() bool { n, _ := a.Store.CountActiveRuns(ctx); return n == 0 })
}
