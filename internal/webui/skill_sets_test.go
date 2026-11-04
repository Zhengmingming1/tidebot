package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func webSetBindings(t *testing.T, c *client) []tasksvc.SkillBinding {
	t.Helper()
	out := []tasksvc.SkillBinding{}
	for _, name := range []string{"lead", "assist"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n# "+name), 0o600); err != nil {
			t.Fatal(err)
		}
		sk, err := c.s.deps.Skills.ImportDir(context.Background(), name, dir, "test")
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tasksvc.SkillBinding{SkillID: sk.ID, Mode: "pinned", VersionID: sk.CurrentVersion.ID})
	}
	out[0].Role = "primary"
	out[1].Role = "auxiliary"
	return out
}

func TestSkillSetCRUDConflictAndDelete(t *testing.T) {
	c := login(t)
	bs := webSetBindings(t, c)
	code, set := c.json(http.MethodPost, "/api/skill-sets", map[string]any{"name": "Composition", "skill_bindings": bs})
	if code != 201 {
		t.Fatalf("create %d %v", code, set)
	}
	id := set["id"].(string)
	vid := set["version_id"].(string)
	if code, _ = c.json(http.MethodPut, "/api/skill-sets/"+id, map[string]any{"name": "stale", "skill_bindings": bs}); code != 409 {
		t.Fatalf("unprotected PUT %d", code)
	}
	code, updated := c.json(http.MethodPut, "/api/skill-sets/"+id, map[string]any{"name": "New name", "skill_bindings": bs, "expected_version_id": vid})
	if code != 200 || updated["version_no"] != float64(2) {
		t.Fatalf("update %d %v", code, updated)
	}
	if rr := c.do(http.MethodGet, "/api/skill-sets/"+id+"/export", "", nil); rr.Code != 200 || rr.Header().Get("Content-Type") != "application/zip" {
		t.Fatal("export failed", rr.Code)
	}
	if code, _ = c.json(http.MethodDelete, "/api/skill-sets/"+id, nil); code != 200 {
		t.Fatal(code)
	}
	if code, _ = c.json(http.MethodGet, "/api/skill-sets/"+id, nil); code != 404 {
		t.Fatal("deleted set readable", code)
	}
	if _, err := c.s.deps.Skills.ResolveBindings(context.Background(), bs); err != nil {
		t.Fatal("deleted member skills", err)
	}
}

func TestTaskCompositionEditAndTransfer(t *testing.T) {
	c := login(t)
	bs := webSetBindings(t, c)
	body := map[string]any{"name": "Composed task", "prompt": "do work", "provider": "anthropic", "skill_bindings": bs}
	code, task := c.json(http.MethodPost, "/api/tasks", body)
	if code != 201 {
		t.Fatalf("create %d %v", code, task)
	}
	id := task["id"].(string)
	_, form := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if len(form["form"].(map[string]any)["skill_bindings"].([]any)) != 2 {
		t.Fatal("form dropped helpers")
	}
	// An older caller changing the prompt must not erase auxiliary bindings.
	delete(body, "skill_bindings")
	body["skill_id"] = bs[0].SkillID
	body["skill_version_id"] = bs[0].VersionID
	body["prompt"] = "edited"
	if code, out := c.json(http.MethodPut, "/api/tasks/"+id, body); code != 200 {
		t.Fatalf("legacy edit %d %v", code, out)
	}
	got, _ := c.s.deps.Tasks.Get(context.Background(), id)
	if len(got.Spec.SkillBindings) != 2 || got.Spec.SkillBindings[1].VersionID != bs[1].VersionID {
		t.Fatalf("helpers lost %+v", got.Spec.SkillBindings)
	}
	rr := c.do(http.MethodGet, "/api/tasks/export?ids="+id, "", nil)
	var file taskFile
	if json.Unmarshal(rr.Body.Bytes(), &file) != nil || file.Version != taskFileVersion || len(file.Tasks[0].Skills) != 2 {
		t.Fatal("composition not portable", rr.Body.String())
	}
	code, out := c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": file, "apply": true})
	if code != 200 || out["created"] != float64(1) {
		t.Fatalf("import %d %v", code, out)
	}
	newID := out["tasks"].([]any)[0].(map[string]any)["task_id"].(string)
	copy, _ := c.s.deps.Tasks.Get(context.Background(), newID)
	if len(copy.Spec.SkillBindings) != 2 || copy.Spec.SkillBindings[0].VersionID != bs[0].VersionID || copy.Spec.SkillBindings[1].Role != "auxiliary" {
		t.Fatalf("transfer changed selection: %+v", copy.Spec.SkillBindings)
	}
	file.Tasks[0].Skills[1].ID = "missing"
	file.Tasks[0].Skills[1].Name = "absent"
	_, out = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": file, "apply": true})
	if out["created"] != float64(0) {
		t.Fatal("silently dropped missing dependency")
	}
	// New clients explicitly sending [] can clear a whole composition.
	body["skill_bindings"] = []tasksvc.SkillBinding{}
	if code, _ = c.json(http.MethodPut, "/api/tasks/"+id, body); code != 200 {
		t.Fatal(code)
	}
	got, _ = c.s.deps.Tasks.Get(context.Background(), id)
	if len(got.Spec.SkillBindings) != 0 {
		t.Fatal("explicit clearing failed")
	}
}

func TestSkillSetPartialErrorResponse(t *testing.T) {
	c := login(t)
	rr := httptest.NewRecorder()
	c.s.setError(rr, &skillstore.PartialSetImportError{ImportedSkillIDs: []string{"member-one"}, Err: errors.New("fixture")})
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 400 || out["partial"] != true || len(out["imported_skill_ids"].([]any)) != 1 {
		t.Fatalf("partial detail lost: %d %s", rr.Code, rr.Body.String())
	}
}

func TestCompositionRerunKeepsExactSnapshot(t *testing.T) {
	c := login(t)
	bs := webSetBindings(t, c)
	ctx := context.Background()
	if _, err := c.s.deps.DB.Exec(`INSERT INTO runs(id,user_id,status,trigger_kind,prompt,workspace_dir,created_at,skill_id,skill_version_id) VALUES('combined_run','owner','succeeded','quick','same task','','2026-01-01',?,?)`, bs[0].SkillID, bs[0].VersionID); err != nil {
		t.Fatal(err)
	}
	if err := c.s.deps.Skills.RecordRunBindings(ctx, "combined_run", bs); err != nil {
		t.Fatal(err)
	}
	set, err := c.s.deps.Skills.SaveSet(ctx, "", "rerun composition", "", bs)
	if err != nil {
		t.Fatal(err)
	}
	source := &tasksvc.SkillSetSource{SetID: set.ID, VersionID: set.VersionID}
	if err = c.s.deps.Skills.RecordRunSource(ctx, "combined_run", source, bs); err != nil {
		t.Fatal(err)
	}
	var got RunRequest
	c.s.deps.StartRun = func(req RunRequest) (string, error) { got = req; return "new_run", nil }
	if code, out := c.json(http.MethodPost, "/api/runs/combined_run/rerun", nil); code != 202 {
		t.Fatalf("rerun %d %v", code, out)
	}
	if got.SkillSetSource == nil || got.SkillSetSource.VersionID != set.VersionID {
		t.Fatal("rerun lost composition source")
	}
	if len(got.SkillBindings) != 2 || got.SkillBindings[1].VersionID != bs[1].VersionID || got.SkillBindings[0].Role != "primary" {
		t.Fatalf("rerun lost exact members: %+v", got)
	}
	_, detail := c.json(http.MethodGet, "/api/runs/combined_run", nil)
	if detail["skill_set_source"].(map[string]any)["version_id"] != set.VersionID {
		t.Fatal("detail lost source")
	}
	for _, raw := range detail["skill_evidence"].([]any) {
		ev := raw.(map[string]any)
		if ev["level"] != "loaded" || ev["execution_observed"] != false {
			t.Fatal("overclaimed skill use", ev)
		}
	}
	if len(detail["skill_bindings"].([]any)) != 2 {
		t.Fatal("detail lost snapshot")
	}
	if _, err := c.s.deps.DB.Exec(`DELETE FROM run_skill_bindings WHERE run_id='combined_run'`); err != nil {
		t.Fatal(err)
	}
	_, detail = c.json(http.MethodGet, "/api/runs/combined_run", nil)
	if len(detail["skill_evidence"].([]any)) != 0 {
		t.Fatal("legacy metadata became execution evidence")
	}
	if len(detail["skill_bindings"].([]any)) != 1 {
		t.Fatal("legacy primary was not reconstructed")
	}
}
