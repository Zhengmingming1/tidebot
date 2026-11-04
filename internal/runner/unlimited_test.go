package runner

import (
	"context"
	"testing"
	"time"
)

func TestUnlimitedRunStillUserCancellable(t *testing.T) {
	skipOnWindows(t)
	r, _, dir := drainRunner(t, 1)
	o := drainOpts(slowClaude(t, dir, "10"), dir)
	o.Timeout = 0
	ids := make(chan string, 1)
	o.OnStart = func(id string) { ids <- id }
	done := make(chan Result, 1)
	go func() { res, _ := r.RunOnce(context.Background(), o); done <- res }()
	var id string
	select {
	case id = <-ids:
	case <-time.After(5 * time.Second):
		t.Fatal("did not start")
	}
	if !r.Cancel(id) {
		t.Fatal("not cancellable")
	}
	select {
	case res := <-done:
		if res.Status == "timeout" || res.Status == "succeeded" {
			t.Fatalf("bad stop status: %s", res.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
}
