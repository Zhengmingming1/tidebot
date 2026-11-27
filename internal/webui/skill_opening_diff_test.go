package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSkillVersionsOpeningDiff: /versions?diff=1 carries the comparison the skill page opens
// with, so the page needs no second round trip: current against its parent (current, not the
// newest), the pair the link names when both belong to the skill, nothing extra without diff=1.
func TestSkillVersionsOpeningDiff(t *testing.T) {
	c := login(t)
	importSkill(t, c, "---\nname: Opener\ndescription: d\n---\none\n", "")
	var skills []map[string]any
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/skills", "", nil).Body.Bytes(), &skills)
	var sid string
	for _, s := range skills {
		if s["name"] == "Opener" {
			sid = s["id"].(string)
		}
	}
	base := "/api/skills/" + sid + "/versions"

	// One version: it is compared with itself (the page shows "same version"), no files read.
	_, v := c.json(http.MethodGet, base+"?diff=1", nil)
	d, _ := v["diff"].(map[string]any)
	vs := v["versions"].([]any)
	v1 := vs[0].(map[string]any)["id"].(string)
	if d == nil || d["a"] != v1 || d["b"] != v1 || len(d["files"].([]any)) != 0 {
		t.Fatalf("single version diff %v", v)
	}

	importSkill(t, c, "---\nname: Opener\ndescription: d\n---\ntwo\n", "version")
	importSkill(t, c, "---\nname: Opener\ndescription: d\n---\nthree\n", "version")
	_, v = c.json(http.MethodGet, base, nil)
	if _, ok := v["diff"]; ok {
		t.Fatalf("diff sent without diff=1: %v", v)
	}
	vs = v["versions"].([]any)
	v2, v3 := vs[1].(map[string]any)["id"].(string), vs[2].(map[string]any)["id"].(string)
	_, d = c.json(http.MethodGet, base+"?diff=1", nil)
	if d = d["diff"].(map[string]any); d["a"] != v2 || d["b"] != v3 || d["added"].(float64) != 1 || d["removed"].(float64) != 1 {
		t.Fatalf("default pair %v", d)
	}
	// Rolled back to v2: the page opens on v2 against its parent v1, not on the newest.
	if code, out := c.json(http.MethodPost, "/api/skills/"+sid+"/rollback", map[string]string{"version_id": v2}); code != 200 {
		t.Fatalf("rollback %d %v", code, out)
	}
	_, d = c.json(http.MethodGet, base+"?diff=1", nil)
	if d = d["diff"].(map[string]any); d["a"] != v1 || d["b"] != v2 {
		t.Fatalf("after rollback %v", d)
	}
	// The pair the link names is the one compared; the same as /diff answers for it.
	_, d = c.json(http.MethodGet, base+"?diff=1&a="+v3+"&b="+v1, nil)
	_, want := c.json(http.MethodGet, "/api/skills/"+sid+"/diff?a="+v3+"&b="+v1, nil)
	if d = d["diff"].(map[string]any); d["a"] != v3 || d["b"] != v1 || d["added"] != want["added"] || d["removed"] != want["removed"] {
		t.Fatalf("named pair %v want %v", d, want)
	}
	// A pair that is not this skill's falls back to the default instead of failing the page.
	_, d = c.json(http.MethodGet, base+"?diff=1&a=nope&b="+v1, nil)
	if d = d["diff"].(map[string]any); d["a"] != v1 || d["b"] != v2 {
		t.Fatalf("foreign pair %v", d)
	}
}

// TestSkillDetailRequests: the skill page asks for the canary comparison only while a canary
// runs (it used to be refused on every visit), takes its opening diff from the version list,
// and the prefetch asks for the same version list the page does.
func TestSkillDetailRequests(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	for _, want := range []string{
		`var vp = api(skillVersionsPath(id, params)), pp = rpc("skills.proposals", { skill_id: id })`,
		`return x[0].canary || (x[1] || []).some(function (p) { return p.status === "canary"; }) ? rpc("skills.compare", { skill_id: id })`,
		`var firstDiff = tab === "diff" && vers.diff && vers.diff.a === a && vers.diff.b === b ? vers.diff : null;`,
		`pfGet(skillVersionsPath(id, null))`,
		`if (g === dgen) { host.classList.remove("busy"); host.innerHTML = diffHtml(d); }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if n := strings.Count(src, `rpc("skills.compare"`); n != 1 {
		t.Errorf("skills.compare asked from %d places, want only the canary branch", n)
	}
	runNode(t, `
const assert = require("node:assert/strict");
function enc(v) { return encodeURIComponent(String(v)); }
eval(FN);
var P = function (q) { return new URLSearchParams(q); };
assert.equal(skillVersionsPath("s 1", null), "/api/skills/s%201/versions?diff=1");
assert.equal(skillVersionsPath("s1", P("")), "/api/skills/s1/versions?diff=1");
assert.equal(skillVersionsPath("s1", P("a=x&b=y z")), "/api/skills/s1/versions?diff=1&a=x&b=y%20z");
assert.equal(skillVersionsPath("s1", P("a=x")), "/api/skills/s1/versions?diff=1", "one side alone is not a pair");
assert.equal(skillVersionsPath("s1", P("tab=files&a=x&b=y")), "/api/skills/s1/versions", "the Files tab needs no diff");
`, map[string]string{"FN": cut(t, src, "  function skillVersionsPath(id, params) {", "  // Same requests the page's render starts with")})
}
