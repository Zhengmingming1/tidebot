package workflow

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func skill(id string, needs ...Dependency) Node {
	return Node{ID: id, Kind: "skill", Prompt: "{{input}}", SkillBindings: []tasksvc.SkillBinding{{SkillID: "skill_test", Mode: "follow"}}, Needs: needs}
}
func service(t *testing.T, exec Executor) *Service {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(sqlDB, exec)
	t.Cleanup(func() {
		s.BeginShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Wait(ctx)
		sqlDB.Close()
	})
	return s
}
func save(t *testing.T, s *Service, nodes ...Node) Workflow {
	t.Helper()
	w, err := s.Save(context.Background(), "", 0, Definition{Name: "Test workflow", MaxParallel: 2, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func start(t *testing.T, s *Service, w Workflow) string {
	t.Helper()
	rid, err := s.Start(context.Background(), w.ID, "request", "mock")
	if err != nil {
		t.Fatal(err)
	}
	return rid
}
func finished(t *testing.T, s *Service, rid string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetRun(context.Background(), rid)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "queued" && r.Status != "running" {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("workflow did not finish")
	return Run{}
}
func state(r Run, id string) NodeRun {
	for _, n := range r.Nodes {
		if n.NodeID == id {
			return n
		}
	}
	return NodeRun{}
}

func TestValidationRejectsInvalidGraphs(t *testing.T) {
	tests := map[string][]Node{
		"cycle":           {skill("a", Dependency{NodeID: "b"}), skill("b", Dependency{NodeID: "a"})},
		"missing":         {skill("a", Dependency{NodeID: "missing"})},
		"duplicate":       {skill("a"), skill("a")},
		"self":            {skill("a", Dependency{NodeID: "a"})},
		"branch on skill": {skill("a"), skill("b", Dependency{NodeID: "a", Branch: "true"})},
	}
	for name, nodes := range tests {
		t.Run(name, func(t *testing.T) {
			d := Definition{Name: "test", Nodes: nodes}
			if Validate(&d) == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
	for _, prompt := range []string{"{{steps.b.output}}", "{{env.HOME}}", "{{input", "{{steps.a.output}}"} {
		d := Definition{Name: "test", Nodes: []Node{skill("a"), skill("b")}}
		d.Nodes[0].Prompt = prompt
		if Validate(&d) == nil {
			t.Fatalf("invalid reference accepted: %s", prompt)
		}
	}
	d := Definition{Name: "test", Nodes: []Node{skill("a"), skill("b", Dependency{NodeID: "a"})}}
	d.Nodes[1].Prompt = "{{input}} then {{steps.a.output}}"
	if err := Validate(&d); err != nil {
		t.Fatal(err)
	}
	d.Nodes[1].Prompt = `Return nested JSON like {"answer":{"ok":true}} for {{input}}`
	if err := Validate(&d); err != nil {
		t.Fatalf("literal JSON is not a template error: %v", err)
	}
}

func TestCanvasPositionsRoundTripWithoutChangingExecutionOrder(t *testing.T) {
	var order []string
	s := service(t, func(ctx context.Context, n Node, prompt, provider string, _ func(string)) (string, error) {
		order = append(order, n.ID)
		return n.ID, nil
	})
	a, b := skill("a"), skill("b", Dependency{NodeID: "a"})
	a.Position = &Position{X: 900, Y: 300}
	b.Position = &Position{X: 10, Y: 20}
	w := save(t, s, b, a)
	r := finished(t, s, start(t, s, w))
	if r.Status != "succeeded" || strings.Join(order, ",") != "a,b" || r.Snapshot.Nodes[0].Position.X != 10 || r.Snapshot.Nodes[1].Position.Y != 300 {
		t.Fatalf("position changed execution or was lost: %+v %v", r, order)
	}
	for _, p := range []Position{{X: -1}, {X: 10001}, {Y: math.Inf(1)}, {X: math.NaN()}} {
		n := skill("a")
		n.Position = &p
		d := Definition{Name: "bad", Nodes: []Node{n}}
		if err := Validate(&d); err == nil {
			t.Fatalf("invalid canvas coordinates accepted: %+v", p)
		}
	}
}

func TestParallelJoinAndImmutableVersions(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan string, 2)
	var running, max atomic.Int32
	s := service(t, func(ctx context.Context, n Node, prompt, provider string, onStart func(string)) (string, error) {
		if n.ID == "merge" {
			if prompt != "left=result:a;right=result:b" {
				return "", errors.New("wrong merged input: " + prompt)
			}
			return "merged", nil
		}
		count := running.Add(1)
		defer running.Add(-1)
		for {
			m := max.Load()
			if count <= m || max.CompareAndSwap(m, count) {
				break
			}
		}
		entered <- n.ID
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return "result:" + n.ID, nil
	})
	a, b := skill("a"), skill("b")
	merge := skill("merge", Dependency{NodeID: "a"}, Dependency{NodeID: "b"})
	merge.Prompt = "left={{steps.a.output}};right={{steps.b.output}}"
	w := save(t, s, a, b, merge)
	rid := start(t, s, w)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("nodes did not run in parallel")
		}
	}
	changed := w.Definition
	changed.Name = "Updated workflow"
	if _, err := s.Save(context.Background(), w.ID, w.Version, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), w.ID, w.Version, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale write accepted: %v", err)
	}
	close(release)
	r := finished(t, s, rid)
	if r.Status != "succeeded" || state(r, "merge").Output != "merged" || max.Load() != 2 || r.WorkflowVersion != 1 || r.Snapshot.Name != "Test workflow" {
		t.Fatalf("bad execution: %+v max=%d", r, max.Load())
	}
	var versions int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM workflow_versions WHERE workflow_id=?`, w.ID).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("history %d %v", versions, err)
	}
}

func TestBranchSkipAnyMergeAndAllJoin(t *testing.T) {
	var mu sync.Mutex
	called := map[string]string{}
	s := service(t, func(ctx context.Context, n Node, prompt, provider string, _ func(string)) (string, error) {
		mu.Lock()
		called[n.ID] = prompt
		mu.Unlock()
		return n.ID + ":" + prompt, nil
	})
	cond := Node{ID: "route", Kind: "condition", Condition: &Condition{Source: "{{input}}", Operator: "equals", Value: "request"}}
	yes := skill("yes", Dependency{NodeID: "route", Branch: "true"})
	no := skill("no", Dependency{NodeID: "route", Branch: "false"})
	merge := skill("merge", Dependency{NodeID: "yes"}, Dependency{NodeID: "no"})
	merge.Join = "any"
	merge.Prompt = "{{steps.yes.output}}|{{steps.no.output}}"
	all := skill("all", Dependency{NodeID: "yes"}, Dependency{NodeID: "no"})
	r := finished(t, s, start(t, s, save(t, s, cond, yes, no, merge, all)))
	mu.Lock()
	defer mu.Unlock()
	if r.Status != "succeeded" || state(r, "no").Status != "skipped" || state(r, "all").Status != "skipped" || called["merge"] != "yes:request|" || len(called) != 2 {
		t.Fatalf("branch execution: %+v calls=%v", r, called)
	}
}

func TestFailFastCancelsSiblingAndDoesNotRunDownstream(t *testing.T) {
	sibling := make(chan struct{})
	var childCalled atomic.Bool
	s := service(t, func(ctx context.Context, n Node, prompt, provider string, _ func(string)) (string, error) {
		switch n.ID {
		case "fail":
			<-sibling
			return "", errors.New("fixture failure")
		case "sibling":
			close(sibling)
			<-ctx.Done()
			return "", ctx.Err()
		default:
			childCalled.Store(true)
			return "unexpected", nil
		}
	})
	r := finished(t, s, start(t, s, save(t, s, skill("fail"), skill("sibling"), skill("child", Dependency{NodeID: "fail"}))))
	if r.Status != "failed" || state(r, "sibling").Status != "canceled" || state(r, "child").Status != "canceled" || childCalled.Load() {
		t.Fatalf("bad fail-fast: %+v", r)
	}
}

func TestStopAndShutdownPersistTerminalState(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "shutdown"}[shutdown], func(t *testing.T) {
			entered := make(chan struct{})
			s := service(t, func(ctx context.Context, n Node, prompt, provider string, _ func(string)) (string, error) {
				close(entered)
				<-ctx.Done()
				return "", ctx.Err()
			})
			w := save(t, s, skill("a"), skill("b", Dependency{NodeID: "a"}))
			rid := start(t, s, w)
			<-entered
			want := "canceled"
			if shutdown {
				s.BeginShutdown()
				want = "interrupted"
				if _, err := s.Start(context.Background(), w.ID, "again", "mock"); !errors.Is(err, ErrClosing) {
					t.Fatalf("start during close: %v", err)
				}
			} else if err := s.Stop(rid); err != nil {
				t.Fatal(err)
			}
			r := finished(t, s, rid)
			if r.Status != want || state(r, "b").Status != "canceled" {
				t.Fatalf("shutdown state %+v", r)
			}
		})
	}
}

func TestRecoveryKeepsSuccessfulOutputAndNeverReplays(t *testing.T) {
	s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) {
		t.Error("recovery must never execute")
		return "", nil
	})
	w := save(t, s, skill("a"), skill("b", Dependency{NodeID: "a"}))
	_, err := s.DB.Exec(`INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,input,snapshot_json,created_at) VALUES('old',?,1,'running','','{}',?)`, w.ID, now())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []NodeRun{{NodeID: "a", Status: "succeeded", Output: "kept"}, {NodeID: "b", Status: "running"}} {
		if _, err := s.DB.Exec(`INSERT INTO workflow_run_nodes(workflow_run_id,node_id,status,output) VALUES('old',?,?,?)`, n.NodeID, n.Status, n.Output); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRun(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "interrupted" || state(r, "a").Output != "kept" || state(r, "a").Status != "succeeded" || state(r, "b").Status != "interrupted" {
		t.Fatalf("recovery %+v", r)
	}
}

func TestOutputAndPromptLimitsDoNotSilentlyTruncate(t *testing.T) {
	s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) {
		return strings.Repeat("x", MaxOutputBytes+1), nil
	})
	r := finished(t, s, start(t, s, save(t, s, skill("a"), skill("b", Dependency{NodeID: "a"}))))
	if r.Status != "failed" || !strings.Contains(r.Error, "output exceeds") || state(r, "a").Output != "" || state(r, "b").Status != "canceled" {
		t.Fatalf("oversize output %+v", r)
	}
	if _, err := render("{{input}}{{input}}{{input}}", strings.Repeat("x", MaxOutputBytes), nil, "all"); err == nil {
		t.Fatal("oversize prompt accepted")
	}
	if _, err := render("{{steps.a.output}}", "", map[string]*NodeRun{"a": {Status: "skipped"}}, "all"); err == nil {
		t.Fatal("missing branch output silently accepted by all-join")
	}
	out, err := render("{{input}}", "{{steps.secret.output}}", nil, "all")
	if err != nil || out != "{{steps.secret.output}}" {
		t.Fatal("input was reinterpreted as template")
	}
}

// Unlimited first: no overall time limit unless the owner sets one, and 0 never
// turns into an instant deadline.
func TestWorkflowTimeoutZeroIsUnlimited(t *testing.T) {
	var hadDeadline atomic.Bool
	s := service(t, func(ctx context.Context, n Node, prompt, provider string, onStart func(string)) (string, error) {
		_, ok := ctx.Deadline()
		hadDeadline.Store(ok)
		return "ok", nil
	})
	w := save(t, s, skill("a"))
	if w.Definition.TimeoutMinutes != 0 {
		t.Fatalf("hidden default timeout %d", w.Definition.TimeoutMinutes)
	}
	if r := finished(t, s, start(t, s, w)); r.Status != "succeeded" || hadDeadline.Load() {
		t.Fatalf("unlimited run: %s deadline=%v", r.Status, hadDeadline.Load())
	}
	limited := w.Definition
	limited.TimeoutMinutes = 5
	w2, err := s.Save(context.Background(), w.ID, w.Version, limited)
	if err != nil || w2.Definition.TimeoutMinutes != 5 {
		t.Fatalf("limit not kept: %v %+v", err, w2.Definition)
	}
	if r := finished(t, s, start(t, s, w2)); r.Status != "succeeded" || !hadDeadline.Load() {
		t.Fatalf("limited run: %s deadline=%v", r.Status, hadDeadline.Load())
	}
	for _, bad := range []int{-1, 1441} {
		d := limited
		d.TimeoutMinutes = bad
		if _, err := s.Save(context.Background(), "", 0, d); err == nil {
			t.Fatalf("accepted timeout %d", bad)
		}
	}
}
