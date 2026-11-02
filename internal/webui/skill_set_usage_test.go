package webui

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
	"net/http"
	"testing"
)

func TestSkillSetUsagePreviewUpgradeRollbackAndConflict(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	bs := webSetBindings(t, c)
	set, err := c.s.deps.Skills.SaveSet(ctx, "", "v1", "", bs)
	if err != nil {
		t.Fatal(err)
	}
	src := &tasksvc.SkillSetSource{SetID: set.ID, VersionID: set.VersionID}
	task, err := c.s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: "linked task", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	sp := *task.Spec
	sp.SkillBindings = set.SkillBindings
	sp.SkillSetSource = src
	task, err = c.s.deps.Tasks.UpdateSpec(ctx, task.Task.ID, sp, "user", "attach")
	if err != nil {
		t.Fatal(err)
	}
	c.s.deps.Workflows = workflow.New(c.s.deps.DB, nil)
	flow, err := c.s.deps.Workflows.Save(ctx, "", 0, workflow.Definition{Name: "linked workflow", Nodes: []workflow.Node{{ID: "first", Kind: "skill", Prompt: "work", SkillBindings: set.SkillBindings, SkillSetSource: src}}})
	if err != nil {
		t.Fatal(err)
	}
	swapped := append([]tasksvc.SkillBinding(nil), set.SkillBindings...)
	swapped[0].Role, swapped[1].Role = "auxiliary", "primary"
	newer, err := c.s.deps.Skills.SaveSet(ctx, set.ID, "v2", set.VersionID, swapped)
	if err != nil {
		t.Fatal(err)
	}
	code, preview := c.json(http.MethodGet, "/api/skill-set-usage?set_id="+set.ID, nil)
	if code != 200 || len(preview["usages"].([]any)) != 2 || len(preview["versions"].([]any)) != 2 {
		t.Fatalf("preview %d %v", code, preview)
	}
	for _, raw := range preview["usages"].([]any) {
		u := raw.(map[string]any)
		if u["state"] != "linked" || u["can_apply"] != true || len(u["changes"].([]any)) != 2 {
			t.Fatal(u)
		}
	}
	body := map[string]any{"target": "task", "id": task.Task.ID, "expected_version_id": task.CurrentVersion.ID, "target_set_version_id": newer.VersionID}
	code, out := c.json(http.MethodPost, "/api/skill-set-usage/apply", body)
	if code != 200 {
		t.Fatalf("apply %d %v", code, out)
	}
	if code, _ = c.json(http.MethodPost, "/api/skill-set-usage/apply", body); code != 409 {
		t.Fatalf("stale apply %d", code)
	}
	updated, _ := c.s.deps.Tasks.Get(ctx, task.Task.ID)
	if updated.Spec.SkillSetSource.VersionID != newer.VersionID || updated.Spec.SkillBindings[0].SkillID != set.SkillBindings[1].SkillID {
		t.Fatal("upgrade lost source or members")
	}
	body["expected_version_id"] = updated.CurrentVersion.ID
	body["target_set_version_id"] = set.VersionID
	if code, out = c.json(http.MethodPost, "/api/skill-set-usage/apply", body); code != 200 {
		t.Fatalf("rollback %d %v", code, out)
	}
	updated, _ = c.s.deps.Tasks.Get(ctx, task.Task.ID)
	updated.Spec.SkillBindings = updated.Spec.SkillBindings[:1]
	updated, err = c.s.deps.Tasks.UpdateSpec(ctx, task.Task.ID, *updated.Spec, "user", "local edit")
	if err != nil {
		t.Fatal(err)
	}
	body["expected_version_id"] = updated.CurrentVersion.ID
	body["target_set_version_id"] = newer.VersionID
	if code, _ = c.json(http.MethodPost, "/api/skill-set-usage/apply", body); code != 400 {
		t.Fatalf("modified overwrite %d", code)
	}
	body["force"] = true
	if code, _ = c.json(http.MethodPost, "/api/skill-set-usage/apply", body); code != 400 {
		t.Fatalf("forced overwrite %d", code)
	}
	wfbody := map[string]any{"target": "workflow_node", "id": flow.ID, "node_id": "first", "expected_version": flow.Version, "target_set_version_id": newer.VersionID}
	if code, out = c.json(http.MethodPost, "/api/skill-set-usage/apply", wfbody); code != 200 {
		t.Fatalf("workflow apply %d %v", code, out)
	}
	if code, _ = c.json(http.MethodPost, "/api/skill-set-usage/apply", wfbody); code != 409 {
		t.Fatalf("stale workflow apply %d", code)
	}
}

func TestTaskSourceOmittedPreservedAndNullDetached(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	bs := webSetBindings(t, c)
	set, err := c.s.deps.Skills.SaveSet(ctx, "", "source", "", bs)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"name": "task", "prompt": "work", "skill_bindings": set.SkillBindings, "skill_set_source": map[string]string{"set_id": set.ID, "version_id": set.VersionID}}
	code, created := c.json(http.MethodPost, "/api/tasks", body)
	if code != 201 {
		t.Fatalf("create %d %v", code, created)
	}
	id := created["id"].(string)
	delete(body, "skill_set_source")
	if code, out := c.json(http.MethodPut, "/api/tasks/"+id, body); code != 200 {
		t.Fatalf("legacy update %d %v", code, out)
	}
	current, _ := c.s.deps.Tasks.Get(ctx, id)
	if current.Spec.SkillSetSource == nil {
		t.Fatal("legacy edit dropped provenance")
	}
	body["skill_set_source"] = nil
	if code, out := c.json(http.MethodPut, "/api/tasks/"+id, body); code != 200 {
		t.Fatalf("detach %d %v", code, out)
	}
	current, _ = c.s.deps.Tasks.Get(ctx, id)
	if current.Spec.SkillSetSource != nil {
		t.Fatal("explicit detach was ignored")
	}
	body["skill_set_source"] = map[string]string{"set_id": "wrong", "version_id": set.VersionID}
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, body); code != 400 {
		t.Fatalf("forged source accepted %d", code)
	}
}

func TestTaskSourceChangeDoesNotMutateComparisonSnapshot(t *testing.T) {
	old := &tasksvc.SkillSetSource{SetID: "set_old", VersionID: "ver_old"}
	spec := tasksvc.Spec{Prompt: "work", SkillSetSource: old}
	next := specFromForm(spec, taskForm{Prompt: "work", SkillSetSource: []byte(`{"set_id":"set_new","version_id":"ver_new"}`)})
	if spec.SkillSetSource.SetID != "set_old" || next.SkillSetSource.SetID != "set_new" {
		t.Fatal("source edit aliased the original version")
	}
}
