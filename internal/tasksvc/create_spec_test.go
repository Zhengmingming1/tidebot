package tasksvc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/version"
)

// A task created with its skills, run mode, effort, constraints and tools has them
// all in version 1: there is no v1 without them in the history to roll back to.
func TestCreateWithSpecIsOneVersion(t *testing.T) {
	s, ctx := atomicFixture(t)
	sp := Spec{
		Prompt:        "ignored: the input's prompt wins",
		SkillBindings: []SkillBinding{{SkillID: "skill_a", SkillName: "a"}},
		RunMode:       "exec", Effort: "high",
		Constraints:  []SpecConstraint{{ID: "c1", Text: "中文输出", Source: "import"}},
		AllowedTools: []string{"WebFetch"}, MaxBudgetUSD: 2,
	}
	tv, err := s.Create(ctx, CreateInput{Name: "t", Prompt: "p", Goal: "g", WorkspaceDir: "/w", TimeoutMinutes: 5,
		Spec: &sp, Reason: "imported"})
	if err != nil {
		t.Fatal(err)
	}
	vs, err := s.Ver.ListVersions(ctx, version.EntityTask, tv.Task.ID)
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions %d %v", len(vs), err)
	}
	var got Spec
	if err := json.Unmarshal([]byte(vs[0].ContentJSON), &got); err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "p" || got.Goal != "g" || got.WorkspaceDir != "/w" || got.TimeoutMinutes != 5 ||
		len(got.SkillBindings) != 1 || got.SkillBindings[0].Role != "primary" || got.SkillBindings[0].Mode != "follow" ||
		got.RunMode != "exec" || got.Effort != "high" || len(got.Constraints) != 1 || len(got.AllowedTools) != 1 || got.MaxBudgetUSD != 2 {
		t.Fatalf("v1 %+v", got)
	}
	if g, _ := s.Get(ctx, tv.Task.ID); g.Spec == nil || g.Spec.Effort != "high" || g.Task.Prompt != "p" || g.Task.TimeoutMinutes != 5 {
		t.Fatalf("view %+v", g)
	}
	if !hasReason(t, s, tv.Task.ID, "imported") {
		t.Fatal("reason not recorded")
	}
	// without a reason it is still "create task"
	plain, err := s.Create(ctx, CreateInput{Name: "plain", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasReason(t, s, plain.Task.ID, "create task") {
		t.Fatal("default reason not recorded")
	}
}

func hasReason(t *testing.T, s *Service, taskID, reason string) bool {
	t.Helper()
	ops, err := s.Ver.ListOpLog(context.Background(), version.EntityTask, taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Reason.String == reason {
			return true
		}
	}
	return false
}

// A spec Create would refuse is refused before anything is written, with the same
// checks UpdateSpec makes.
func TestCreateWithBadSpecWritesNothing(t *testing.T) {
	s, ctx := atomicFixture(t)
	bad := []Spec{
		{RunMode: "root"},
		{Effort: "max"},
		{SkillBindings: []SkillBinding{{SkillID: "skill_a"}, {SkillID: "skill_a"}}},
		{SkillBindings: []SkillBinding{{SkillID: "skill_a", Mode: "pinned"}}},
	}
	for i := range bad {
		in := CreateInput{Name: "t", Prompt: "p", Spec: &bad[i]}
		if err := s.CheckCreate(in); err == nil {
			t.Fatalf("CheckCreate accepted %+v", bad[i])
		}
		if _, err := s.Create(ctx, in); err == nil {
			t.Fatalf("Create accepted %+v", bad[i])
		}
	}
	// a workflow task cannot also bind skills
	wf := Spec{SkillBindings: []SkillBinding{{SkillID: "skill_a"}}}
	if _, err := s.Create(ctx, CreateInput{Name: "w", WorkflowID: "wf_1", Spec: &wf}); err == nil {
		t.Fatal("workflow task with skills accepted")
	}
	var n int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tasks written: %d %v", n, err)
	}
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM versions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("versions written: %d %v", n, err)
	}
}
