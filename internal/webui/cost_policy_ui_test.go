package webui

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"testing"
)

func TestCostPolicyFormModesAndConflict(t *testing.T) {
	c := login(t)
	code, created := c.json("POST", "/api/tasks", map[string]any{"name": "cost modes", "prompt": "hello", "enabled": true})
	if code != 201 {
		t.Fatalf("create %d %v", code, created)
	}
	id := created["id"].(string)
	for _, mode := range []string{"global", "bypass", "task"} {
		_, raw := c.json("GET", "/api/tasks/"+id+"/form", nil)
		form := raw["form"].(map[string]any)
		policy := form["cost_control"].(map[string]any)
		policy["mode"] = mode
		policy["run_amount"] = 0.4
		policy["daily_amount"] = 2.5
		code, result := c.json("PUT", "/api/tasks/"+id, form)
		if code != 200 {
			t.Fatalf("save %s %d %v", mode, code, result)
		}
		cp, err := c.s.deps.Tasks.GetCostPolicy(context.Background(), id)
		if err != nil || cp.Mode != mode || cp.RunAmount == nil || *cp.RunAmount != .4 || cp.DailyAmount == nil || *cp.DailyAmount != 2.5 {
			t.Fatalf("policy %+v %v", cp, err)
		}
		if code, _ := c.json("PUT", "/api/tasks/"+id, form); code != 409 {
			t.Fatalf("stale revision %d", code)
		}
	}
	_, raw := c.json("GET", "/api/tasks/"+id+"/form", nil)
	form := raw["form"].(map[string]any)
	form["cost_control"].(map[string]any)["mode"] = "bad"
	if code, _ := c.json("PUT", "/api/tasks/"+id, form); code != 400 {
		t.Fatalf("bad mode %d", code)
	}
}

func TestCostPolicyImportBypassAndLegacyPreservation(t *testing.T) {
	c := login(t)
	one, day := 1.0, 7.1
	file := taskFile{Kind: taskFileKind, Version: taskFileVersion, Tasks: []taskFileEntry{{Name: "import bypass", Prompt: "hello", CostControl: &tasksvc.CostPolicy{Mode: "bypass", Currency: "CNY", RunAmount: &one, DailyAmount: &day}}}}
	code, res := c.json("POST", "/api/tasks/import", map[string]any{"file": file, "apply": true, "pick": []int{0}})
	if code != 200 || res["created"].(float64) != 1 {
		t.Fatalf("import %d %v", code, res)
	}
	tasks, err := c.s.deps.Tasks.List(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks %v %v", tasks, err)
	}
	id := tasks[0].Task.ID
	cp, err := c.s.deps.Tasks.GetCostPolicy(context.Background(), id)
	if err != nil || cp.Mode != "bypass" || cp.Currency != "USD" || cp.DailyAmount == nil || *cp.DailyAmount != 1 {
		t.Fatalf("converted %+v %v", cp, err)
	}
	_, raw := c.json("GET", "/api/tasks/"+id+"/form", nil)
	form := raw["form"].(map[string]any)
	delete(form, "cost_control")
	if code, res := c.json("PUT", "/api/tasks/"+id, form); code != 200 {
		t.Fatalf("legacy %d %v", code, res)
	}
	cp, _ = c.s.deps.Tasks.GetCostPolicy(context.Background(), id)
	if cp.Mode != "bypass" {
		t.Fatalf("legacy resets mode %+v", cp)
	}
}

func TestCostPolicyStaleEditorDoesNotChangeTask(t *testing.T) {
	c := login(t)
	_, created := c.json("POST", "/api/tasks", map[string]any{"name": "original", "prompt": "original prompt", "enabled": true})
	id := created["id"].(string)
	_, raw := c.json("GET", "/api/tasks/"+id+"/form", nil)
	form := raw["form"].(map[string]any)
	mode := "global"
	if _, err := c.s.deps.Tasks.UpdateCostPolicy(context.Background(), id, tasksvc.CostPolicyUpdate{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	form["name"] = "should not save"
	form["prompt"] = "should not save"
	if code, _ := c.json("PUT", "/api/tasks/"+id, form); code != 409 {
		t.Fatalf("stale status %d", code)
	}
	task, err := c.s.deps.Tasks.Get(context.Background(), id)
	if err != nil || task.Task.Name != "original" || task.Spec.Prompt != "original prompt" {
		t.Fatalf("partial save %+v %v", task, err)
	}
}

func TestCostPolicyCreateConflictLeavesNoActiveTask(t *testing.T) {
	c := login(t)
	code, res := c.json("POST", "/api/tasks", map[string]any{"name": "stale creation", "prompt": "hello", "enabled": true, "cost_control": map[string]any{"mode": "bypass", "currency": "USD", "fx_revision": "stale"}})
	if code != 409 {
		t.Fatalf("stale create %d %v", code, res)
	}
	tasks, err := c.s.deps.Tasks.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Task.Status != "archived" {
			t.Fatalf("incomplete live task %+v", task.Task)
		}
	}
}

func TestCostPolicyWrongCurrencyDoesNotChangeTask(t *testing.T) {
	c := login(t)
	_, created := c.json("POST", "/api/tasks", map[string]any{"name": "original", "prompt": "original prompt", "enabled": true})
	id := created["id"].(string)
	_, raw := c.json("GET", "/api/tasks/"+id+"/form", nil)
	form := raw["form"].(map[string]any)
	form["cost_control"].(map[string]any)["currency"] = "CNY"
	form["name"] = "should not save"
	form["prompt"] = "should not save"
	if code, _ := c.json("PUT", "/api/tasks/"+id, form); code != 409 {
		t.Fatalf("wrong currency status %d", code)
	}
	task, err := c.s.deps.Tasks.Get(context.Background(), id)
	if err != nil || task.Task.Name != "original" || task.Spec.Prompt != "original prompt" {
		t.Fatalf("partial save %+v %v", task, err)
	}
}
