package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
	"testing"
)

func TestCompositionSourceSurvivesQuickTaskAndWorkflowRuns(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ctx := context.Background()
	primary, helper := importTestSkill(t, a, "primary"), importTestSkill(t, a, "helper")
	set, err := a.Skills.SaveSet(ctx, "", "linked", "", []tasksvc.SkillBinding{{SkillID: primary}, {SkillID: helper}})
	if err != nil {
		t.Fatal(err)
	}
	source := &tasksvc.SkillSetSource{SetID: set.ID, VersionID: set.VersionID}
	check := func(rid string) {
		t.Helper()
		snapshot, err := a.Skills.RunSource(ctx, rid)
		if err != nil || snapshot == nil || snapshot.VersionID != set.VersionID || snapshot.State != "linked" {
			t.Fatalf("run %s source %+v %v", rid, snapshot, err)
		}
		bs, err := a.Skills.RunBindings(ctx, rid)
		if err != nil || len(bs) != 2 || bs[1].VersionID != set.SkillBindings[1].VersionID {
			t.Fatalf("run %s bindings %+v %v", rid, bs, err)
		}
	}
	rid, err := a.startRun(webui.RunRequest{Prompt: "work", SkillBindings: set.SkillBindings, SkillSetSource: source})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, rid)
	check(rid)
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "linked task", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	tv.Spec.SkillBindings, tv.Spec.SkillSetSource = set.SkillBindings, source
	tv, err = a.Tasks.UpdateSpec(ctx, tv.Task.ID, *tv.Spec, "user", "link")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.runTaskTriggerOn(ctx, tv.Task, "manual", "", func(id string) { rid = id }); err != nil {
		t.Fatal(err)
	}
	check(rid)
	node := workflow.Node{ID: "a", Kind: "skill", Prompt: "work", SkillBindings: set.SkillBindings, SkillSetSource: source}
	if _, err = a.executeWorkflowNode(ctx, node, "work", "", func(id string) { rid = id }); err != nil {
		t.Fatal(err)
	}
	check(rid)
}

func TestWorkflowSourceStateIsCapturedBeforeFreezingFollowBindings(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ctx := context.Background()
	primary, helper := importTestSkill(t, a, "primary"), importTestSkill(t, a, "helper")
	set, err := a.Skills.SaveSet(ctx, "", "source", "", []tasksvc.SkillBinding{{SkillID: primary}, {SkillID: helper}})
	if err != nil {
		t.Fatal(err)
	}
	source := &tasksvc.SkillSetSource{SetID: set.ID, VersionID: set.VersionID}
	follow := append([]tasksvc.SkillBinding(nil), set.SkillBindings...)
	follow[0].Mode = "follow"
	follow[0].VersionID = ""
	prepared, err := a.prepareWorkflow(ctx, workflow.Definition{Nodes: []workflow.Node{{ID: "a", Kind: "skill", SkillBindings: follow, SkillSetSource: source}}})
	if err != nil {
		t.Fatal(err)
	}
	node := prepared.Nodes[0]
	if node.SkillSetState != "modified" || node.SkillBindings[0].Mode != "pinned" {
		t.Fatalf("snapshot %+v", node)
	}
	var rid string
	if _, err = a.executeWorkflowNode(ctx, node, "work", "", func(id string) { rid = id }); err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.Skills.RunSource(ctx, rid)
	if err != nil || snapshot.State != "modified" {
		t.Fatalf("lost declared modification: %+v %v", snapshot, err)
	}
}
