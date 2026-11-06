package webui

import "testing"

// Use the shipped request/cache/prefetch code together: a stalled read RPC must
// reject and release its shared pending entry and prefetch slot, allowing retry.
func TestReadRequestDeadlines(t *testing.T) {
	js := appJS(t)
	runNode(t, `
const assert = require("node:assert/strict");
var window = { AbortController }, state = { csrf: "csrf" }, location = { hash: "#/tasks" };
var document = { hidden: false }, navigator = {}, timers = new Map(), nextTimer = 0, calls = [];
function T(k) { return k; }
function enc(v) { return encodeURIComponent(v); }
function setTimeout(fn, ms) { var id = ++nextTimer; timers.set(id, { fn, ms }); return id; }
function clearTimeout(id) { timers.delete(id); }
function netFetch(path, init) {
  calls.push({ path, init });
  return new Promise(function (resolve, reject) {
    if (!init.signal) return;
    function abort() { var e = new Error("aborted"); e.name = "AbortError"; reject(e); }
    if (init.signal.aborted) abort(); else init.signal.addEventListener("abort", abort, { once: true });
  });
}
eval(API);
eval(RPC);
eval(PREFETCH);
function fireDeadline(ms) {
  assert.equal(timers.size, 1, "one request deadline");
  var entry = timers.values().next().value;
  assert.equal(entry.ms, ms); entry.fn();
}
async function timedRead(request) {
  fireDeadline(30000);
  await assert.rejects(request, e => e.timeout && e.message === "err.req_timeout");
  assert.equal(timers.size, 0, "deadline cleared after failure");
}
(async function () {
  await timedRead(apiRaw("/api/tasks"));
  await timedRead(apiRaw("/api/read", { method: "POST", read: true }));
  await timedRead(apiRaw("/api/read", { method: "POST", cacheKey: "read" }));
  for (var method of ["dashboard", "scheduler.pause.get", "free_pool.status", "free_pool.models", "plugins.trigger_fields", "plugins.ai.status", "plugins.ai.messages", "update.status"]) {
    await timedRead(rpc(method));
  }
  await timedRead(rpcRead("skills.optimize_settings", { skill_id: "s1" }));

  // Explicit deadlines override the read default; zero disables it.
  var request = apiRaw("/api/tasks", { timeout: 7000 });
  fireDeadline(7000); await assert.rejects(request, e => e.timeout);
  apiRaw("/api/tasks", { timeout: 0 });
  assert.equal(timers.size, 0); assert.equal(calls.at(-1).init.signal, undefined);
  // Writes (including AI/install/scan) retain their existing caller-chosen deadline.
  for (var method of ["skills.canary_start", "plugins.ai.generate", "plugins.install_official", "storage.cleanup"]) {
    rpc(method); assert.equal(timers.size, 0, method + " acquired a read deadline");
  }
  apiRaw("/api/scan", { method: "POST" }); assert.equal(timers.size, 0);

  // External cancellation keeps its AbortError, and releases the read timer.
  var ac = new AbortController();
  request = apiRaw("/api/read", { method: "POST", read: true, signal: ac.signal });
  ac.abort(); await assert.rejects(request, e => e.name === "AbortError" && !e.timeout);
  assert.equal(timers.size, 0);

  // A stalled response body has the same deadline as the initial read.
  var pendingFetch = netFetch;
  netFetch = (path, init) => Promise.resolve({ headers: { get: () => "application/json" }, ok: true, status: 200,
    json: () => new Promise((resolve, reject) => init.signal.addEventListener("abort", () => {
      var e = new Error("aborted body"); e.name = "AbortError"; reject(e);
    }, { once: true })) });
  request = rpc("dashboard"); await Promise.resolve(); await Promise.resolve();
  await timedRead(request); netFetch = pendingFetch;

  // A timed-out shared page read is removed; a retry gets a new network request.
  swr.on = true;
  request = rpc("dashboard"); var key = rpcKey("dashboard");
  assert(swr.pending[key]); await timedRead(request);
  assert.equal(swr.pending[key], undefined);
  var before = calls.length;
  request = rpc("dashboard"); assert.equal(calls.length, before + 1); await timedRead(request);
  swr.on = false;

  // Exercise real skill-prefetch RPCs, including the caller that used to omit read:true.
  assert.equal(prefetchRoute("#/skills/s1"), 5);
  var prefetched = Object.values(swr.pending);
  assert.equal(pf.busy, 5); assert.equal(timers.size, 5);
  for (var timer of Array.from(timers.values())) { assert.equal(timer.ms, 30000); timer.fn(); }
  var results = await Promise.allSettled(prefetched);
  assert(results.every(r => r.status === "rejected" && r.reason.timeout));
  assert.equal(pf.busy, 0); assert.equal(Object.keys(swr.pending).length, 0);
  assert.equal(timers.size, 0); assert.equal(Object.keys(swr.cache).length, 0);
  assert.equal(prefetchRoute("#/skills/s1"), 5, "retry can refill prefetch slots");
  prefetched = Object.values(swr.pending);
  for (var timer of Array.from(timers.values())) timer.fn();
  await Promise.allSettled(prefetched);
})().catch(e => { console.error(e); process.exitCode = 1; });
`, map[string]string{
		"API":      cut(t, js, "  var swr = { on: false", "  // Toasts:"),
		"RPC":      cut(t, js, "  // Pure reads:", "  function renderPlugins() {"),
		"PREFETCH": cut(t, js, "  // ---------- intent prefetch ----------", "  (function () {\n    var clear = function ()"),
	})
}
