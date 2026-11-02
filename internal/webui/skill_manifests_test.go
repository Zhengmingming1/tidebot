package webui

import (
	"context"
	"net/http"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func TestSkillManifestAPIAndDependencyAdoption(t *testing.T) {
	c := login(t)
	bs := webSetBindings(t, c)
	code, current := c.json(http.MethodGet, "/api/skill-manifests/"+bs[0].SkillID, nil)
	if code != 200 || current["canonical_name"] != "lead" {
		t.Fatalf("get %d %v", code, current)
	}
	manifest := skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Dependencies: []skillstore.SkillDependency{{SkillName: "assist"}}}
	code, edited := c.json(http.MethodPut, "/api/skill-manifests/"+bs[0].SkillID, map[string]any{"expected_version_id": current["version_id"], "manifest": manifest})
	if code != 200 || edited["version_id"] == current["version_id"] {
		t.Fatalf("put %d %v", code, edited)
	}
	if code, _ = c.json(http.MethodPut, "/api/skill-manifests/"+bs[0].SkillID, map[string]any{"expected_version_id": current["version_id"], "manifest": manifest}); code != 409 {
		t.Fatal("stale manifest overwrite", code)
	}
	bs[0].VersionID = edited["version_id"].(string)
	code, preview := c.json(http.MethodPost, "/api/skill-sets/preview", map[string]any{"skill_bindings": bs[:1]})
	if code != 200 || preview["resolvable"] != true || preview["ready"] != false || len(preview["additions"].([]any)) != 1 {
		t.Fatalf("preview %d %v", code, preview)
	}
	code, preview = c.json(http.MethodPost, "/api/skill-sets/preview", map[string]any{"skill_bindings": bs})
	if code != 200 || preview["ready"] != true {
		t.Fatalf("adopt %d %v", code, preview)
	}
	details := skillstore.SkillSetDetails{Description: "Review with helper", TestCases: []skillstore.EvalCase{{Name: "sample", Input: "sample input", MustContain: []string{"expected"}}}}
	code, set := c.json(http.MethodPost, "/api/skill-sets", map[string]any{"name": "capability", "skill_bindings": bs, "details": details})
	if code != 201 || set["details"].(map[string]any)["description"] != details.Description {
		t.Fatalf("create %d %v", code, set)
	}
	code, set = c.json(http.MethodPut, "/api/skill-sets/"+set["id"].(string), map[string]any{"name": "renamed", "skill_bindings": bs, "expected_version_id": set["version_id"]})
	if code != 200 || set["details"].(map[string]any)["description"] != details.Description {
		t.Fatalf("legacy update %d %v", code, set)
	}
}

func TestSkillSetDependencyPreviewError(t *testing.T) {
	c := login(t)
	bs := webSetBindings(t, c)
	sk, err := c.s.deps.Skills.GetSkillManifest(context.Background(), bs[0].SkillID, bs[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := c.s.deps.Skills.SaveSkillManifest(context.Background(), bs[0].SkillID, sk.VersionID, skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Dependencies: []skillstore.SkillDependency{{SkillName: "missing"}}})
	if err != nil {
		t.Fatal(err)
	}
	bs[0].VersionID = edited.VersionID
	code, out := c.json(http.MethodPost, "/api/skill-sets", map[string]any{"name": "incomplete", "skill_bindings": bs})
	if code != 400 || out["dependency_preview"] == nil {
		t.Fatalf("structured dependency error missing %d %v", code, out)
	}
	if out["dependency_preview"].(map[string]any)["resolvable"] != false {
		t.Fatal(out)
	}
	if rr := c.do(http.MethodPut, "/api/skill-manifests/"+bs[0].SkillID, "application/json", []byte(`{"expected_version_id":"x","manifest":{"format":"tidebot-skill/1","shell":"never"}}`)); rr.Code != 400 {
		t.Fatal("unknown manifest field accepted", rr.Code)
	}
	if code, _ := c.json(http.MethodPost, "/api/skill-sets/preview", map[string]any{"skill_bindings": []tasksvc.SkillBinding{}}); code != 200 {
		t.Fatal(code)
	}
}
