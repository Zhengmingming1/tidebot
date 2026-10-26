package webui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// cut returns src from the first from up to (not including) the next to.
func cut(t *testing.T, src, from, to string) string {
	t.Helper()
	i := strings.Index(src, from)
	if i < 0 {
		t.Fatalf("app.js lost %q", from)
	}
	j := strings.Index(src[i:], to)
	if j < 0 {
		t.Fatalf("app.js: %q not followed by %q", from, to)
	}
	return src[i : i+j]
}

// TestIntentPrefetch: hovering / pressing / focusing a link loads that page's first requests
// into the page cache, the page then renders from them without a second trip; refusals stand
// for a moment, writes in flight discard the answer, read-only RPCs share the cache and no
// longer drop it, and nothing is prefetched for the current page, signed out, on Save-Data,
// in a hidden tab, for filtered addresses or past the in-flight cap.
func TestIntentPrefetch(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`if (res.status === 401 && path !== "/api/login" && !opts.quiet) { showLogin();`,
		`return Promise.all(taskDetailPaths(id, tab).map(function (u) { return api(u); })).then(function (r) {`,
		`rpcRead("skills.optimize_settings", { skill_id: id })`,
		`document.addEventListener("pointerdown", now, true);`,
		`document.addEventListener("focusin", now);`,
		`if (!a || a.closest("#modal-root")) return "";`,
		"kbLive = kbPage();\n    prefetchRoute(kbCur[kbPage()]);",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	cache := cut(t, src, "  var swr = { on: false", "  function apiRaw(path, opts) {")
	rpcs := cut(t, src, "  // Pure reads:", "  function renderPlugins() {")
	tdp := cut(t, src, "  function taskDetailPaths(id, tab) {", "  function renderTaskDetail(id, tab) {")
	pfb := cut(t, src, "  // ---------- intent prefetch ----------", "  (function () {\n    var clear = function ()")
	script := `
var bad = [], calls = [], reply = {}, held = [], rerenders = 0;
function enc(v) { return encodeURIComponent(String(v == null ? "" : v)); }
var state = { csrf: "tok" }, location = { hash: "#/tasks" }, document = { hidden: false }, navigator = {};
function page() { return null; }
function $() { return { innerHTML: "" }; }
function route() { rerenders++; }
// Fake network: answers from reply[path] (an Error with .status rejects); "hold" keeps it pending.
function apiRaw(path, opts) {
  opts = opts || {};
  calls.push((opts.method || "GET") + " " + path + (opts.quiet ? " quiet" : ""));
  var r = reply[path];
  if (r === "hold") return new Promise(function (ok) { held.push(function () { ok({ late: path }); }); });
  if (r instanceof Error) return Promise.reject(r);
  return Promise.resolve(r === undefined ? { path: path } : r);
}
function err(status) { var e = new Error("no " + status); e.status = status; return e; }
var setTimeout = function (f) { return 0; }, clearTimeout = function () {};
` + cache + rpcs + tdp + pfb + `
function keys(h) { return prefetchReqs(h).map(function (q) { return q.key; }).join(" | "); }
function tick() { return new Promise(function (r) { global.setImmediate(r); }); }
(async function () {
  var T = "#/tasks/t%201", tid = "/api/tasks/t%201";
  if (keys("#/") !== "/api/overview | /api/dashboard") bad.push("home: " + keys("#/"));
  if (keys("#/tasks") !== "/api/tasks") bad.push("tasks: " + keys("#/tasks"));
  if (keys(T) !== [tid, tid + "/amendments", tid + "/versions", tid + "/runs?limit=6"].join(" | ")) bad.push("task: " + keys(T));
  if (keys(T + "/runs").indexOf("runs?limit=50") < 0) bad.push("task runs tab: " + keys(T + "/runs"));
  if (keys("#/runs/r1") !== "/api/runs/r1") bad.push("run: " + keys("#/runs/r1"));
  if (keys("#/skills/s1") !== '/api/skills | /api/skills/s1/versions | rpc:skills.optimize_settings:{"skill_id":"s1"} | rpc:skills.proposals:{"skill_id":"s1"} | rpc:skills.compare:{"skill_id":"s1"}') bad.push("skill: " + keys("#/skills/s1"));
  ["#/runs", "#/runs?status=failed", "#/tasks?f=failing", "#/settings/general", "#/tasks/%E0%A4", "#/spend"].forEach(function (h) { if (keys(h)) bad.push("should skip " + h + ": " + keys(h)); });

  // Guards.
  state.csrf = ""; if (prefetchRoute("#/runs/r1")) bad.push("prefetched signed out"); state.csrf = "tok";
  if (prefetchRoute("#/tasks")) bad.push("prefetched the current page");
  document.hidden = true; if (prefetchRoute("#/runs/r1")) bad.push("prefetched in a hidden tab"); document.hidden = false;
  navigator.connection = { saveData: true }; if (prefetchRoute("#/runs/r1")) bad.push("prefetched on Save-Data"); navigator.connection = null;
  if (calls.length) bad.push("guards still fetched: " + calls);

  // Prefetch, then the page render uses it: no second request.
  if (prefetchRoute(T) !== 4) bad.push("task prefetch count");
  if (prefetchRoute(T) !== 0) bad.push("in-flight prefetch repeated");
  await tick();
  if (calls.length !== 4 || calls.some(function (c) { return c.indexOf(" quiet") < 0; })) bad.push("prefetch calls: " + calls);
  calls = []; swr.on = true; swr.soft = false;
  var got = await Promise.all(taskDetailPaths("t 1", "overview").map(function (u) { return api(u); }));
  swr.on = false;
  if (calls.length || got[0].path !== tid) bad.push("render refetched after prefetch: " + calls);
  if (prefetchRoute(T) !== 0) bad.push("cached page prefetched again");

  // An old prefetched entry is served but revalidated, like any cached page.
  swr.cache[tid].t -= SWR_PF_MS + 1; calls = []; swr.on = true;
  await api(tid); swr.on = false; await tick();
  if (calls.join() !== "GET " + tid) bad.push("stale prefetch not revalidated: " + calls);

  // A 4xx is kept for a moment (no second ask); a 5xx is not.
  reply["/api/rpc/skills.compare"] = err(400); reply["/api/runs/r5"] = err(500);
  prefetchRoute("#/skills/s1"); prefetchRoute("#/runs/r5"); await tick(); await tick();
  calls = []; swr.on = true;
  var r1 = await rpc("skills.compare", { skill_id: "s1" }).then(function () { return "ok"; }, function (e) { return e.status; });
  var r2 = await api("/api/runs/r5").then(function () { return "ok"; }, function (e) { return e.status; });
  swr.on = false;
  if (r1 !== 400 || r2 !== 500) bad.push("refusal results " + r1 + " " + r2);
  if (calls.join() !== "GET /api/runs/r5") bad.push("refusal caching calls: " + calls);

  // A write while a prefetch is in flight: its answer is not kept.
  reply["/api/runs/r9"] = "hold"; prefetchRoute("#/runs/r9");
  await api("/api/tasks/x/pause", { method: "POST", body: {} });
  held.shift()(); await tick(); await tick();
  if (swr.cache["/api/runs/r9"]) bad.push("kept a prefetch that raced a write");

  // Read-only RPCs share the cache and keep it; writes drop it.
  swr.cache["/api/tasks"] = { s: "[]", t: Date.now() };
  await rpc("plugins.list"); await rpcRead("skills.optimize_settings", { skill_id: "s1" });
  if (!swr.cache["/api/tasks"]) bad.push("a read RPC dropped the page cache");
  calls = []; swr.on = true; var two = Promise.all([rpc("plugins.list"), rpc("plugins.list")]); swr.on = false; await two; await tick();
  if (calls.length !== 1) bad.push("read RPC not deduped/cached: " + calls);
  await rpc("skills.optimize_settings", { skill_id: "s1", mode: "auto" });
  if (swr.cache["/api/tasks"]) bad.push("a write RPC kept the page cache");

  // At most PF_MAX in flight.
  for (var i = 0; i < 9; i++) reply["/api/runs/h" + i] = "hold";
  var n = 0; for (i = 0; i < 9; i++) n += prefetchRoute("#/runs/h" + i);
  if (n !== PF_MAX || pf.busy !== PF_MAX) bad.push("in-flight cap: " + n + " busy " + pf.busy);
  while (held.length) held.shift()();
  await tick(); await tick();
  if (pf.busy !== 0) bad.push("busy not released: " + pf.busy);
  if (rerenders) bad.push("prefetch caused re-renders: " + rerenders);
  console.log(bad.length ? bad.join("\n") : "OK");
})();
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "OK" {
		t.Fatalf("prefetch checks failed: %v\n%s", err, out)
	}
}
