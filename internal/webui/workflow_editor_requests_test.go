package webui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestWorkflowEditorRequests: the workflow editor sends its first requests (run history
// included) in one round through the page cache, the intent prefetch asks for the same keys,
// and opening another node reuses the skill sets and versions the page already has instead
// of asking again each time.
func TestWorkflowEditorRequests(t *testing.T) {
	src := strings.ReplaceAll(appJSSource(t), "\r\n", "\n")
	ed := cut(t, src, "  function renderWorkflowEditor(id) {", "  // Execution page.")
	for _, want := range []string{
		"var paths=workflowEditorPaths(id),hist=creating?null:api(paths[4]);",
		"hist.then(function(rs){",
		"{source:n.skill_set_source,sets:sets,versions:skillVersions}",
	} {
		if !strings.Contains(ed, want) {
			t.Errorf("renderWorkflowEditor lost %q", want)
		}
	}
	if strings.Contains(ed, "api('/api/workflow-runs?workflow_id=") {
		t.Error("run history fetched again after the page is drawn")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	paths := cut(t, src, "  function workflowEditorPaths(id) {", "  function renderWorkflowEditor(id) {")
	editor := cut(t, src, "  var skillEditorSeq = 0;", "  function dependencyPreviewHtml(p) {")
	script := `
var bad = [], calls = [];
function enc(v) { return encodeURIComponent(String(v == null ? "" : v)); }
function T(k) { return k; } function esc(s) { return String(s); } function ic() { return ""; }
function opt(v, l) { return "<option>" + l + "</option>"; } function sectionTabs() { return ""; }
function el() { return { addEventListener: function () {} }; }
function $() { return el(); } function $$() { return []; }
var document = { contains: function () { return true; } };
function Event() {}
function api(path) { calls.push(path); return Promise.resolve(path === "/api/skill-sets" ? [{ id: "set1", name: "S", version_no: 1 }] : { versions: [{ id: "v1", no: 1 }] }); }
` + paths + editor + `
function tick() { return new Promise(function (r) { global.setImmediate(r); }); }
(async function () {
  var p = workflowEditorPaths("w 1").join(" | ");
  if (p !== "/api/skills | /api/skill-sets | /api/workflows/w%201 | /api/settings | /api/workflow-runs?workflow_id=w%201") bad.push("paths: " + p);
  p = workflowEditorPaths("new").filter(Boolean).join(" | ");
  if (p !== "/api/skills | /api/skill-sets | /api/settings") bad.push("new paths: " + p);
  var skills = [{ id: "sk", name: "repo-summary" }], init = [{ skill_id: "sk", mode: "follow" }];
  // A standalone editor (task form) still loads skill sets and versions itself.
  var box = { innerHTML: "", dispatchEvent: function () {} };
  skillBindingEditor(box, skills, init, null, {}); await tick();
  if (calls.join() !== "/api/skills/sk/versions,/api/skill-sets") bad.push("standalone: " + calls);
  if (box.innerHTML.indexOf("S · v1") < 0) bad.push("standalone editor lost the loaded set");
  // The workflow editor passes what it has: one versions request for the page, none per node.
  calls = []; var shared = {}, sets = [{ id: "set1", name: "Mine", version_no: 2 }];
  for (var i = 0; i < 3; i++) { box.innerHTML = ""; skillBindingEditor(box, skills, init, null, { sets: sets, versions: shared }); await tick(); }
  if (calls.join() !== "/api/skills/sk/versions") bad.push("node switches asked again: " + calls);
  if (box.innerHTML.indexOf("Mine · v2") < 0 || box.innerHTML.indexOf("bt.pin_v") < 0) bad.push("shared sets/versions not shown: " + box.innerHTML);
  console.log(bad.length ? bad.join("\n") : "OK");
})();
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "OK" {
		t.Fatalf("workflow editor checks failed: %v\n%s", err, out)
	}
}
