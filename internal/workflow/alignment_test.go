package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Nodes sharing a writable folder run one at a time; read-only nodes keep the
// parallelism. The executor sees the run (task, snapshot) it belongs to.
func TestWritingWorkflowSerializesNodes(t *testing.T) {
	for _, mode := range []string{"read", "exec", "edit"} {
		var running, peak atomic.Int32
		var mu sync.Mutex
		var seen []RunInfo
		s := service(t, func(ctx context.Context, n Node, _, _ string, _ func(string)) (string, error) {
			ri, _ := RunFrom(ctx)
			mu.Lock()
			seen = append(seen, ri)
			mu.Unlock()
			if v := running.Add(1); v > peak.Load() {
				peak.Store(v)
			}
			time.Sleep(60 * time.Millisecond)
			running.Add(-1)
			return n.ID, nil
		})
		w, err := s.Save(context.Background(), "", 0, Definition{Name: "W", MaxParallel: 3, RunMode: mode, WorkspaceDir: " /tmp/x ", Nodes: []Node{skill("a"), skill("b"), skill("c")}})
		if err != nil {
			t.Fatal(err)
		}
		if w.Definition.WorkspaceDir != "/tmp/x" {
			t.Fatalf("folder not trimmed: %q", w.Definition.WorkspaceDir)
		}
		rid, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Input: "in", Provider: "mock", TaskID: "task_1", Bucket: "production"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WaitRun(context.Background(), rid); err != nil {
			t.Fatal(err)
		}
		if r := finished(t, s, rid); r.Status != "succeeded" {
			t.Fatalf("%s: %s %s", mode, r.Status, r.Error)
		}
		want := int32(1)
		if mode == "read" {
			want = 3
		}
		if peak.Load() != want {
			t.Fatalf("%s: peak parallel %d, want %d", mode, peak.Load(), want)
		}
		for _, ri := range seen {
			if ri.RunID != rid || ri.TaskID != "task_1" || ri.Bucket != "production" || ri.Def.RunMode != mode {
				t.Fatalf("run info: %+v", ri)
			}
		}
	}
}

func TestWorkflowBudgetStopsFurtherNodes(t *testing.T) {
	var calls atomic.Int32
	s := service(t, func(ctx context.Context, n Node, _, _ string, _ func(string)) (string, error) {
		calls.Add(1)
		return n.ID, nil
	})
	s.OverBudget = func(_ context.Context, _ string, d Definition) string {
		if d.Budget != 1.5 || d.BudgetCurrency != "USD" {
			t.Errorf("budget %v %s", d.Budget, d.BudgetCurrency)
		}
		if calls.Load() >= 1 {
			return "spent 1.60 of 1.50 USD"
		}
		return ""
	}
	w, err := s.Save(context.Background(), "", 0, Definition{Name: "W", RunMode: "exec", Budget: 1.5, BudgetCurrency: "USD", Nodes: []Node{skill("a"), skill("b", Dependency{NodeID: "a"})}})
	if err != nil {
		t.Fatal(err)
	}
	r := finished(t, s, start(t, s, w))
	if r.Status != "failed" || !strings.Contains(r.Error, ErrBudget.Error()) || !strings.Contains(r.Error, "1.60 of 1.50") {
		t.Fatalf("run: %s %q", r.Status, r.Error)
	}
	if calls.Load() != 1 || r.Nodes[1].Status != "canceled" {
		t.Fatalf("calls %d, node b %s", calls.Load(), r.Nodes[1].Status)
	}
	// No budget: never asked.
	s.OverBudget = func(context.Context, string, Definition) string { t.Error("asked without a budget"); return "x" }
	w2, _ := s.Save(context.Background(), "", 0, Definition{Name: "W2", Nodes: []Node{skill("a")}})
	if r := finished(t, s, start(t, s, w2)); r.Status != "succeeded" {
		t.Fatal(r.Status, r.Error)
	}
}

func TestWorkflowSettingsValidateAndVersionStart(t *testing.T) {
	bad := []Definition{
		{RunMode: "admin"},
		{Budget: -1},
		{Budget: 5},
		{Budget: 5, BudgetCurrency: "EUR"},
	}
	for _, d := range bad {
		d.Name, d.Nodes = "W", []Node{skill("a")}
		if err := Validate(&d); err == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
	d := Definition{Name: "W", Budget: 0, BudgetCurrency: "USD", Nodes: []Node{skill("a")}}
	if err := Validate(&d); err != nil || d.BudgetCurrency != "" {
		t.Fatal(err, d.BudgetCurrency)
	}
	var prompts []string
	var mu sync.Mutex
	s := service(t, func(_ context.Context, n Node, p, _ string, _ func(string)) (string, error) {
		mu.Lock()
		prompts = append(prompts, n.Prompt)
		mu.Unlock()
		return "", nil
	})
	w := save(t, s, skill("a"))
	v2 := w.Definition
	v2.Nodes = []Node{skill("a")}
	v2.Nodes[0].Prompt = "second {{input}}"
	if _, err := s.Save(context.Background(), w.ID, 1, v2); err != nil {
		t.Fatal(err)
	}
	rid, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Version: 1, Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if r := finished(t, s, rid); r.WorkflowVersion != 1 || r.Status != "succeeded" {
		t.Fatal(r.WorkflowVersion, r.Status)
	}
	if _, err := s.StartWith(context.Background(), StartOptions{WorkflowID: w.ID, Version: 9}); err == nil {
		t.Fatal("unknown version started")
	}
	if len(prompts) != 1 || prompts[0] != "{{input}}" {
		t.Fatalf("pinned version not used: %v", prompts)
	}
	// Admit refuses: nothing starts and release is not leaked.
	s.Admit = func(context.Context, string, Definition) (func(), error) { return nil, errors.New("folder busy") }
	if _, err := s.Start(context.Background(), w.ID, "", ""); err == nil || err.Error() != "folder busy" {
		t.Fatal(err)
	}
	released := make(chan struct{})
	s.Admit = func(context.Context, string, Definition) (func(), error) { return func() { close(released) }, nil }
	rid = start(t, s, w)
	finished(t, s, rid)
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("release not called")
	}
}
