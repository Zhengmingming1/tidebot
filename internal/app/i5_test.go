package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTriggerTestEventEnableAndLastFire(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tid := mkTask(t, a, "hooked")
	res, err := a.createTrigger(ctx, TriggerBinding{Kind: "webhook", TaskID: tid})
	if err != nil {
		t.Fatal(err)
	}
	bid := res["id"].(string)
	call := func(m string, v any) (any, error) { b, _ := json.Marshal(v); return a.CallExt(ctx, m, b) }
	if _, err := call("triggers.test", map[string]any{"id": bid}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return runCount(a, tid) == 1 })
	var prompt string
	_ = a.DB.QueryRow(`SELECT prompt FROM runs WHERE task_id=?`, tid).Scan(&prompt)
	if !strings.Contains(prompt, `"test":true`) {
		t.Fatalf("test payload missing: %q", prompt)
	}
	b, _ := a.getTrigger(ctx, bid)
	if b.LastFiredAt == "" || b.LastResult != "accepted" || b.FireCount != 1 {
		t.Fatalf("last fire not recorded: %+v", b)
	}
	// rate limit shows up as the last result too
	if _, err := call("triggers.test", map[string]any{"id": bid}); err == nil {
		t.Fatal("rate limit not applied")
	}
	b, _ = a.getTrigger(ctx, bid)
	if b.LastResult != "rate limited" || b.FireCount != 2 {
		t.Fatalf("rate-limited fire not recorded: %+v", b)
	}
	if _, err := call("triggers.enable", map[string]any{"id": bid, "enabled": false}); err != nil {
		t.Fatal(err)
	}
	a.trig.lastFire = map[string]time.Time{}
	if _, err := call("triggers.test", map[string]any{"id": bid}); err == nil {
		t.Fatal("disabled trigger fired")
	}
	if _, err := call("triggers.enable", map[string]any{"id": "nope", "enabled": true}); err == nil {
		t.Fatal("unknown trigger enabled")
	}
}

func TestBudgetWarningOncePerDay(t *testing.T) {
	a := newTestApp(t)
	ch := a.Events.Subscribe(8)
	a.warnOnce("global spend at 85% of daily USD cap")
	a.warnOnce("global spend at 90% of daily USD cap")
	n := 0
	for {
		select {
		case e := <-ch:
			if e.Kind == EventBudgetWarning {
				n++
			}
			continue
		case <-time.After(100 * time.Millisecond):
		}
		break
	}
	if n != 1 {
		t.Fatalf("want one warning, got %d", n)
	}
	if m, ok := a.EventToMessage(Event{Kind: EventBudgetWarning, Detail: "x"}); !ok || m.Title == "" {
		t.Fatal("warning has no message")
	}
}
