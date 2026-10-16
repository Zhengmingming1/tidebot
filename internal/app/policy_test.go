package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetryThenBreaker(t *testing.T) {
	a := newTestApp(t)
	a.retryScale = 0.0001
	ctx := context.Background()
	task, err := a.EnsureTask(ctx, "flaky", "do it", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ch := a.Events.Subscribe(64)
	// overloaded is retryable: 1 failure then success → 2 runs, counter reset
	cnt := filepath.Join(t.TempDir(), "cnt")
	_ = os.WriteFile(cnt, []byte("1"), 0o644)
	t.Setenv("TIDEBOT_MOCK_FAIL_MSG", "API Error: overloaded_error")
	t.Setenv("TIDEBOT_MOCK_FAIL_COUNTER", cnt)
	a.runWithPolicy(ctx, task, "cron", "production", 0)
	waitFor(t, 5*time.Second, func() bool {
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status='succeeded'`, task.ID).Scan(&n)
		var p int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM task_policies WHERE task_id=?`, task.ID).Scan(&p)
		return n == 1 && p == 1
	})
	// non-retryable failures trip the breaker after 3
	t.Setenv("TIDEBOT_MOCK_FAIL_COUNTER", "")
	t.Setenv("TIDEBOT_MOCK_FAIL_MSG", "authentication_failed")
	for i := 0; i < 3; i++ {
		a.runWithPolicy(ctx, task, "cron", "production", 0)
	}
	t.Log("policy", a.Policy.Get(ctx, task.ID))
	tk, _ := a.Store.GetTask(ctx, task.ID)
	if tk.Status != "paused" {
		t.Fatalf("status=%s", tk.Status)
	}
	found := false
	for len(ch) > 0 {
		if e := <-ch; e.Kind == EventBreakerTripped {
			found = true
		}
	}
	if !found {
		t.Fatal("no breaker event")
	}
}
