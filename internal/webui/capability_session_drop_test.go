package webui

import (
	"os"
	"strings"
	"testing"
)

// TestCapabilityNewSessionDropped: install / rescan / update / smoke test / import each
// make a blank session for their card first. When the request after it fails, the empty
// session is deleted again (as a blank chat whose first send fails), the error still
// reaches the caller, and a session that got its card is kept.
func TestCapabilityNewSessionDropped(t *testing.T) {
	b, err := os.ReadFile("src/capabilities.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	helper := regexpFunc(t, src, "  function capInNewSession(work) {")
	if n := strings.Count(src, `"/api/chat-sessions", { method: "POST"`) + strings.Count(src, `"/api/chat-sessions",{method:"POST"`); n != 1 {
		t.Fatalf("capabilities.js creates sessions in %d places; all must go through capInNewSession", n)
	}
	for _, want := range []string{
		`return capInNewSession(function (s) {
      return api("/api/capabilities/proposals"`,
		`capInNewSession(function(session){return api("/api/capabilities/"+enc(capID)+"/rescan"`,
		`capInNewSession(function (session) {
            return api("/api/capabilities/" + enc(capID) + "/update"`,
		`capInNewSession(function(session){return api("/api/chat-sessions/"+enc(session.id)+"/bindings"`,
		`return capInNewSession(function (s) {
      return api("/api/capabilities/import"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("capabilities.js lacks %q", want)
		}
	}
	runNode(t, `
function enc(s) { return encodeURIComponent(s); }
var calls = [], createFails = false;
function api(path, o) {
  calls.push((o && o.method || "GET") + " " + path);
  if (path === "/api/chat-sessions" && o.method === "POST") return createFails ? Promise.reject(new Error("create")) : Promise.resolve({ id: "chat_" + calls.length });
  return Promise.resolve({});
}
`+helper+`
var bad = [];
function ok(c, m) { if (!c) bad.push(m); }
(async function () {
  calls = [];
  var r = await capInNewSession(function (s) { return api("/api/capabilities/proposals", { method: "POST" }).then(function () { return s.id; }); });
  ok(r === "chat_1", "success result: " + r);
  ok(calls.join() === "POST /api/chat-sessions,POST /api/capabilities/proposals", "success keeps session: " + calls);

  calls = [];
  var e = null;
  try { await capInNewSession(function () { return Promise.reject(new Error("already_installed")); }); } catch (x) { e = x; }
  ok(e && e.message === "already_installed", "error passed on: " + e);
  ok(calls.join() === "POST /api/chat-sessions,DELETE /api/chat-sessions/chat_1", "failed request drops session: " + calls);

  calls = []; e = null;
  try { await capInNewSession(function () { throw new Error("sync"); }); } catch (x) { e = x; }
  ok(e && e.message === "sync" && calls[1] === "DELETE /api/chat-sessions/chat_1", "sync throw drops session: " + calls);

  calls = []; e = null; createFails = true;
  var ran = false;
  try { await capInNewSession(function () { ran = true; }); } catch (x) { e = x; }
  ok(e && e.message === "create" && !ran && calls.length === 1, "no session, nothing to drop: " + calls);

  createFails = false; calls = []; e = null;
  api = (function (orig) { return function (path, o) { var p = orig(path, o); return o && o.method === "DELETE" ? Promise.reject(new Error("gone")) : p; }; })(api);
  try { await capInNewSession(function () { return Promise.reject(new Error("bad_bundle")); }); } catch (x) { e = x; }
  ok(e && e.message === "bad_bundle", "a failed drop keeps the original error: " + e);
  if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
})();
`, nil)
}
