package workflow

import (
	"context"
	"fmt"
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

// ListRunsByTask returns only executions started by that task, newest first,
// and ignores manual runs and other tasks. An empty id is not "every run".
func TestListRunsByTask(t *testing.T) {
	s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) {
		return "ok", nil
	})
	w := save(t, s, skill("a"))
	first, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Input: "first", Provider: "mock", TaskID: "tsk_night"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Input: "other", Provider: "mock", TaskID: "tsk_other"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Input: "second", Provider: "mock", TaskID: "tsk_night"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(context.Background(), w.ID, "by hand", "mock"); err != nil {
		t.Fatal(err)
	}
	finished(t, s, first)
	finished(t, s, other)
	finished(t, s, second)
	// starts in the same millisecond share created_at and the id's time prefix
	// (seen on Windows CI): give each a distinct start time
	for i, rid := range []string{first, other, second} {
		if _, err := s.DB.Exec(`UPDATE workflow_runs SET created_at=? WHERE id=?`, fmt.Sprintf("2026-11-24T00:00:0%dZ", i), rid); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListRunsByTask(context.Background(), "  tsk_night  ", 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("task list: %d %v", len(list), err)
	}
	if list[0].ID != second || list[1].ID != first {
		t.Fatalf("order: %s %s", list[0].ID, list[1].ID)
	}
	if list[0].TaskID != "tsk_night" || list[0].Snapshot.Name == "" {
		t.Fatalf("row: %+v", list[0])
	}
	one, err := s.ListRunsByTask(context.Background(), "tsk_night", 1)
	if err != nil || len(one) != 1 || one[0].ID != second {
		t.Fatalf("limit: %v %v", one, err)
	}
	empty, err := s.ListRunsByTask(context.Background(), "", 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty id: %d %v", len(empty), err)
	}
	none, err := s.ListRunsByTask(context.Background(), "tsk_missing", 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("missing: %d %v", len(none), err)
	}
}
