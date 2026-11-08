package webui

import (
	"strings"
	"testing"
)

// TestWorkflowRunLivePage: the execution page follows a running workflow without a second fetch
// per change, keeps the sections the reader opened or folded (and output scroll) across a
// repaint, holds new data while text is selected, ignores answers for a page already left, and
// stops polling once the run has ended. It used to keep polling a finished run forever,
// comparing against the stale running snapshot and refetching + repainting every 2 s, which
// reopened every output the reader had folded.
func TestWorkflowRunLivePage(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function wfRunKeep(box) {")
	end := strings.Index(js, "\n  function dependencyFindingHTML(")
	if start < 0 || end < start {
		t.Fatal("workflow run page functions not found")
	}
	runNode(t, `
var hash = "#/workflow-runs/wr1", location = { get hash() { return hash; } };
var window = { scrollY: 0, scrollTo: function (x, y) { window.scrollY = y; } };
// tiny DOM: mount() turns each data-wf-d into a details record; open follows the markup.
var dom = [], pg = { scrollTop: 0 }, mounts = 0, stops = 0, polls = 0, pollFn = null, quiet = true, apiCalls = 0, server = null;
function page() { return pg; }
function mount(html) {
  mounts++; dom = [];
  html.replace(/<details( open)? data-wf-d="([^"]+)">/g, function (_, open, key) { dom.push({ key: key, open: !!open, pre: { scrollTop: 0 }, getAttribute: function () { return this.key; } }); });
}
function $$(sel) { return sel === "details[data-wf-d]" ? dom : []; }
function $(sel, root) { if (sel === "pre") return root.pre; return { onclick: null }; }
function api(path) { apiCalls++; return Promise.resolve(JSON.parse(JSON.stringify(server))); }
function livePoll(fn) { polls++; pollFn = fn; }
function stopPoll() { stops++; pollFn = null; }
function liveQuiet() { return quiet; }
function enc(s) { return s; } function esc(s) { return String(s); } function T(k) { return k; }
function ic() { return ""; } function fmtTime(s) { return s; } function wfStatus(s) { return s; }
function errInfoText(i, e) { return e; } function wfDiagram() { return ""; } function fail() {}
eval(SECTION);
function node(id, status, out) { return { node_id: id, status: status, output: out || "" }; }
function run(status, nodes) { return { id: "wr1", workflow_id: "wf1", workflow_version: 1, status: status, created_at: "t", provider: "p", input: "in", snapshot: { name: "W", nodes: [{ id: "a", name: "A" }, { id: "b", name: "B", skill_bindings: [{ skill_id: "s", role: "primary", version_id: "v" }] }] }, nodes: nodes }; }
function find(k) { return dom.filter(function (d) { return d.key === k; })[0]; }
var bad = [];
(async function () {
  server = run("running", [node("a", "succeeded", "A out"), node("b", "running")]);
  await renderWorkflowRun("wr1");
  if (apiCalls !== 1 || polls !== 1) bad.push("first paint: api " + apiCalls + " polls " + polls);
  // reader opens the input, folds step a's output, scrolls it
  find("input").open = true; find("out:a").open = false; find("out:a").pre.scrollTop = 0;
  // unchanged: no repaint
  await pollFn();
  if (mounts !== 1 || apiCalls !== 2) bad.push("unchanged tick: mounts " + mounts + " api " + apiCalls);
  // a change is painted from the poll's own response, keeping what the reader set
  server = run("running", [node("a", "succeeded", "A out"), node("b", "running", "partial")]);
  find("out:a").open = true; find("out:a").pre.scrollTop = 120; find("out:a").open = false;
  await pollFn();
  if (mounts !== 2 || apiCalls !== 3) bad.push("change: mounts " + mounts + " api " + apiCalls + " (want one request per change)");
  if (!find("input").open) bad.push("input closed by repaint");
  if (find("out:a").open) bad.push("folded output reopened");
  if (find("out:a").pre.scrollTop !== 120) bad.push("output scroll reset");
  if (!find("out:b").open) bad.push("new output should open");
  if (!find("sk:b") || find("sk:b").open) bad.push("skill snapshot section");
  // text selected: the change is held, painted at the next quiet check without a new request
  quiet = false; server = run("running", [node("a", "succeeded", "A out"), node("b", "running", "partial 2")]);
  await pollFn();
  if (mounts !== 2) bad.push("painted while text selected");
  quiet = true; var calls = apiCalls;
  await pollFn();
  if (mounts !== 3 || apiCalls !== calls) bad.push("held data: mounts " + mounts + " api " + (apiCalls - calls));
  // finished: last paint, then polling stops for good
  server = run("succeeded", [node("a", "succeeded", "A out"), node("b", "succeeded", "B out")]);
  await pollFn();
  if (mounts !== 4 || pollFn !== null) bad.push("finished: mounts " + mounts + " poll still on");
  if (find("out:a").open || !find("input").open) bad.push("final paint lost reader state");
  // a late answer for a page already left paints nothing
  server = run("running", [node("a", "running")]);
  await renderWorkflowRun("wr1"); var left = pollFn;
  server = run("running", [node("a", "succeeded", "x")]); hash = "#/runs"; var m = mounts;
  await left();
  if (mounts !== m) bad.push("painted over a page already left");
  if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
})();
`, map[string]string{"SECTION": js[start:end]})
}
