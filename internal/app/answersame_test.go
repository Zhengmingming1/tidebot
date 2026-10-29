package app

import (
	"context"
	"testing"
)

// A task can skip its success notice when the answer is the same as last time; the run
// remembers which earlier run it matched either way.
func TestSuccessNoticeOnlyWhenAnswerChanged(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	task, _ := a.EnsureTask(ctx, "Digest", "p", "", "", "")
	p := a.Policy.Get(ctx, task.ID)
	if p.NotifyChanged {
		t.Fatal("off by default")
	}
	p.NotifySuccess = true
	_ = a.Policy.Put(ctx, p)
	n := 0
	run := func(status, answer string) Event {
		t.Helper()
		n++
		id := "run_s" + string(rune('0'+n))
		if _, err := a.DB.Exec(`INSERT INTO runs(id, task_id, status, prompt, created_at) VALUES(?,?,?,'p',?)`, id, task.ID, status, "2026-10-29T0"+string(rune('0'+n))+":00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := a.Store.SetRunResult(ctx, id, answer); err != nil {
			t.Fatal(err)
		}
		ev := Event{Kind: EventRunFinished, TaskID: task.ID, TaskName: "Digest", RunID: id}
		a.markAnswerSame(ctx, &ev)
		return ev
	}
	notified := func(ev Event) bool { _, ok := a.EventToMessage(ev); return ok }

	// option off: compared and recorded, still notified
	e1 := run("succeeded", "- a\n- b\n")
	e2 := run("succeeded", "- a\r\n\n  - b")
	if e1.AnswerSameAs != "" || e2.AnswerSameAs != "run_s1" || e2.Unchanged || !notified(e2) {
		t.Fatalf("off: %+v %+v", e1, e2)
	}
	var stored string
	_ = a.DB.QueryRow(`SELECT answer_same_as FROM runs WHERE id='run_s2'`).Scan(&stored)
	if stored != "run_s1" {
		t.Fatalf("stored %q", stored)
	}

	p.NotifyChanged = true
	_ = a.Policy.Put(ctx, p)
	if !a.Policy.Get(ctx, task.ID).NotifyChanged {
		t.Fatal("saved")
	}
	e3 := run("succeeded", "- a - b")
	if !e3.Unchanged || notified(e3) {
		t.Fatalf("same answer is quiet: %+v", e3)
	}
	// a failed run in between doesn't count as the previous answer
	run("failed", "")
	e5 := run("succeeded", "- a\n- c")
	if e5.AnswerSameAs != "" || e5.Unchanged || !notified(e5) {
		t.Fatalf("changed answer notifies: %+v", e5)
	}
	// an empty answer is never "the same"
	e6 := run("succeeded", "  \n")
	if e6.Unchanged || !notified(e6) {
		t.Fatalf("empty: %+v", e6)
	}
	// failures are never held back
	if !notified(Event{Kind: EventRunFailed, TaskID: task.ID, RunID: "x", Detail: "boom", Unchanged: true}) {
		t.Fatal("failure")
	}
}

// policy.set from a caller that doesn't send notify_changed keeps it.
func TestPolicySetKeepsNotifyChanged(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	task, _ := a.EnsureTask(ctx, "x", "p", "", "", "")
	if _, err := a.rpcPolicySet(ctx, []byte(`{"task_id":"`+task.ID+`","notify_changed":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.rpcPolicySet(ctx, []byte(`{"task_id":"`+task.ID+`","retry_max":1}`)); err != nil {
		t.Fatal(err)
	}
	if !a.Policy.Get(ctx, task.ID).NotifyChanged {
		t.Fatal("kept")
	}
}
