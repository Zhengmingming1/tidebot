package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// One task paused for a while from the WebUI (task.pause_for) or agentctl (tasks.pause with
// "for"): the answer carries the end, a bad length is refused, and the scheduler's poll
// wakes it once the end has passed — also while every schedule is paused.
func TestTaskPauseFor(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "snz", Prompt: "say hi", CronExpr: "0 9 * * *", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := tv.Task.ID
	call := func(method string, p map[string]any) (map[string]any, error) {
		raw, _ := json.Marshal(p)
		var out any
		var err error
		if method == "task.pause_for" {
			out, err = a.CallExt(ctx, method, raw)
		} else {
			out, err = a.rpcTasksPause(ctx, raw)
		}
		m, _ := out.(map[string]any)
		return m, err
	}
	if _, err := call("task.pause_for", map[string]any{"id": id, "for": "2h"}); err == nil {
		t.Fatal("unknown length accepted")
	}
	out, err := call("task.pause_for", map[string]any{"id": id, "for": "4h"})
	if err != nil {
		t.Fatal(err)
	}
	until, _ := time.Parse(time.RFC3339, out["until"].(string))
	if d := time.Until(until); d < 239*time.Minute || d > 241*time.Minute {
		t.Fatalf("4h ends in %v", d)
	}
	tk, _ := a.Store.GetTask(ctx, id)
	if tk.Status != "paused" || tk.PauseKind != tasksvc.PauseSnooze {
		t.Fatalf("task %+v", tk)
	}
	// agentctl form: "manual" → plain pause by hand, no end in the answer
	if out, err = call("tasks.pause", map[string]any{"id": id, "for": "manual"}); err != nil || out["until"] != nil {
		t.Fatalf("manual %v %v", out, err)
	}
	if tk, _ = a.Store.GetTask(ctx, id); tk.PauseKind != tasksvc.PauseUser {
		t.Fatalf("manual kind %q", tk.PauseKind)
	}

	// the end passes while every schedule is paused: the poll still wakes the task
	if _, err := call("task.pause_for", map[string]any{"id": id, "for": "1h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE tasks SET pause_detail=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetSchedPause(ctx, true, "manual"); err != nil {
		t.Fatal(err)
	}
	a.Sched.Wake(ctx, time.Now())
	if tk, _ = a.Store.GetTask(ctx, id); tk.Status != "active" || tk.PauseKind != "" {
		t.Fatalf("not woken %+v", tk)
	}
}
