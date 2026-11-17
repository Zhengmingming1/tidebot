package workflow

import (
	"context"
	"testing"
)

// A run started by a task stores that task on the workflow run itself (not only
// on the node runs in context); a run started by hand leaves it empty.
func TestWorkflowRunPersistsTask(t *testing.T) {
	s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) {
		return "ok", nil
	})
	w := save(t, s, skill("a"))
	fromTask, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Input: "from task", Provider: "mock", TaskID: "  tsk_night  "})
	if err != nil {
		t.Fatal(err)
	}
	byHand, err := s.Start(context.Background(), w.ID, "by hand", "mock")
	if err != nil {
		t.Fatal(err)
	}
	if r := finished(t, s, fromTask); r.TaskID != "tsk_night" {
		t.Fatalf("task run: task_id=%q status=%s", r.TaskID, r.Status)
	}
	if r := finished(t, s, byHand); r.TaskID != "" {
		t.Fatalf("manual run kept a task: %q", r.TaskID)
	}
	list, err := s.ListRuns(context.Background(), w.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	byID := map[string]string{}
	for _, r := range list {
		byID[r.ID] = r.TaskID
	}
	if byID[fromTask] != "tsk_night" || byID[byHand] != "" {
		t.Fatalf("list task ids: %v", byID)
	}
}
