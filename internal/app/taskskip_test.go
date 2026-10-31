package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// task.skip_run (WebUI and agentctl call): skips the next run by default, a given time, and
// puts it back with undo; bad input is refused.
func TestTaskSkipRunRPC(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "skip", Prompt: "say hi", CronExpr: "0 9 * * *", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := tv.Task.ID
	first := tv.Task.NextRunAt.String
	call := func(p map[string]any) (tasksvc.SkipResult, error) {
		raw, _ := json.Marshal(p)
		out, err := a.CallExt(ctx, "task.skip_run", raw)
		r, _ := out.(tasksvc.SkipResult)
		return r, err
	}
	if _, err := call(map[string]any{}); err == nil {
		t.Fatal("no id accepted")
	}
	if _, err := call(map[string]any{"id": id, "at": "tomorrow"}); err == nil {
		t.Fatal("bad time accepted")
	}
	r, err := call(map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := time.Parse(time.RFC3339, first)
	n, _ := time.Parse(time.RFC3339, r.NextRunAt)
	if r.At != f.UTC().Format(time.RFC3339) || n.Sub(f) < 23*time.Hour || n.Sub(f) > 25*time.Hour {
		t.Fatalf("skip next: %+v (first %s)", r, first)
	}
	if r, err = call(map[string]any{"id": id, "at": r.At, "undo": true}); err != nil || r.NextRunAt != f.UTC().Format(time.RFC3339) || len(r.Skipped) != 0 {
		t.Fatalf("undo: %+v %v", r, err)
	}
	raw, _ := json.Marshal(r)
	if string(raw) == "" || !json.Valid(raw) || r.Skipped == nil {
		t.Fatalf("answer %s", raw)
	}
}
