package webui

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// fakeSignedChats adds the optional fingerprint to fakeChats.
type fakeSignedChats struct {
	fakeChats
	sig  string
	gets int
}

func (f *fakeSignedChats) ChatSessionSig(_ context.Context, id string) (string, error) {
	if id != "chat_1" {
		return "", ErrChatSessionNotFound
	}
	return f.sig, nil
}
func (f *fakeSignedChats) GetChatSession(ctx context.Context, id string) (ChatSessionDetail, error) {
	f.gets++
	return f.fakeChats.GetChatSession(ctx, id)
}

// The session page polls the session every 1.5 s while a turn is answered. A
// matching ?since= gets a short {unchanged} reply without reading the turns.
func TestChatSessionGetSince(t *testing.T) {
	c := login(t)
	f := &fakeSignedChats{sig: "s1"}
	c.s.deps.ChatSessions = f
	code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1", nil)
	if code != 200 || out["sig"] != "s1" || out["turn_list"] == nil || f.gets != 1 {
		t.Fatalf("full: %d %v gets=%d", code, out, f.gets)
	}
	rr := c.do(http.MethodGet, "/api/chat-sessions/chat_1?since=s1", "", nil)
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != `{"sig":"s1","unchanged":true}` || f.gets != 1 {
		t.Fatalf("unchanged: %d %s gets=%d", rr.Code, rr.Body, f.gets)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache header %q", rr.Header().Get("Cache-Control"))
	}
	// a changed session, a stale or empty since: the whole session again
	f.sig = "s2"
	for _, q := range []string{"?since=s1", "?since=", ""} {
		code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1"+q, nil)
		if code != 200 || out["unchanged"] != nil || out["sig"] != "s2" || out["turn_list"] == nil {
			t.Fatalf("%q: %d %v", q, code, out)
		}
	}
	// a missing session is still 404, whatever since says
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_x?since=s2", nil); code != 404 || out["code"] != "not_found" {
		t.Fatalf("missing: %d %v", code, out)
	}
	// an app without the fingerprint keeps answering in full, no sig
	c.s.deps.ChatSessions = &fakeChats{}
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?since=s2", nil); code != 200 || out["unchanged"] != nil || out["sig"] != nil {
		t.Fatalf("unsigned: %d %v", code, out)
	}
}

// TestChatPollSkipsUnchanged runs bindChat's real load/paint under node: a poll sends the
// last sig, an {unchanged} reply renders nothing (no turn is turned into HTML again) but
// still refreshes the capability cards, a changed reply repaints, and a 404 forgets the sig.
func TestChatPollSkipsUnchanged(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function bindChat(")
	if start < 0 {
		t.Fatal("bindChat not found")
	}
	end := strings.Index(js[start+20:], "\n  function") + start + 20
	if end < start+20 {
		t.Fatal("bindChat not found")
	}
	runNode(t, `
function El(id) { this.id = id; this.children = []; this.attrs = {}; this.style = {}; this.classList = { contains: function (c) { return c === "rc-turn"; } }; this.hidden = false; this.value = ""; this.disabled = false; }
El.prototype.addEventListener = function () {};
El.prototype.setAttribute = function (k, v) { this.attrs[k] = v; };
El.prototype.getAttribute = function (k) { return this.attrs[k] == null ? null : this.attrs[k]; };
El.prototype.appendChild = function (c) { this.children.push(c); c.parent = this; return c; };
El.prototype.replaceChild = function (n, o) { this.children[this.children.indexOf(o)] = n; };
El.prototype.remove = function () {};
Object.defineProperty(El.prototype, "firstElementChild", { get: function () { return this.children[0] || null; } });
Object.defineProperty(El.prototype, "lastElementChild", { get: function () { return this.children[this.children.length - 1] || null; } });
Object.defineProperty(El.prototype, "innerHTML", { set: function (h) { this.children = []; this._h = h; var m = /data-turn="(\d+)"/.exec(h); if (m) { this.firstChild = new El(); this.firstChild.attrs["data-turn"] = m[1]; } }, get: function () { return this._h || ""; } });
var els = {}; ["#rc-thread", "#rc-form", "#rc-in", "#rc-send", "#rc-meta", "#rc-err", "#rc-noweb", "#rc-sep"].forEach(function (k) { els[k] = new El(k); });
var sec = new El("sec");
function $(sel, root) { if (els[sel]) return els[sel]; var m = /data-turn="(\d+)"/.exec(sel); if (m) return els["#rc-thread"].children.filter(function (c) { return c.attrs["data-turn"] === m[1]; })[0] || null; return null; }
function $$() { return []; }
var document = { contains: function () { return true; }, createElement: function () { return new El(); } };
var window = { innerHeight: 800 };
function Blob(p) { this.size = p[0].length; }
function T(k) { return k; } function esc(s) { return String(s); } function num(n) { return String(n); } function usd(n) { return String(n); } function enc(s) { return s; }
function draftLoad() { return null; } function draftSave() {} function draftClear() {} function copyText() { return Promise.resolve(); } function fail() {} function stopRun() { return Promise.resolve(); }
function errorState(m) { return m; } function rcErrText(e) { return e.message; }
function rcBusy(st) { return st === "running" || st === "queued"; }
var renders = 0; function rcTurnHtml(t) { renders++; return '<article class="rc-turn" data-turn="' + t.index + '">' + t.status + t.answer + "</article>"; }
function rcTurns(s) { return (s.turn_list || []).filter(function (t) { return t.index > 0; }); }
function rcStream() {} function stopChatStream() {} function rcElapsed() {} var rcTick = 1, RC_POLL_MS = 1500;
var polls = 0; function livePoll() { polls++; } function stopPoll() {}
var capCalls = 0; function capRefreshSession() { capCalls++; }
var requests = [], server = null, status = 200;
function api(path) {
  requests.push(path);
  if (status === 404) { var e = new Error("gone"); e.status = 404; return Promise.reject(e); }
  var m = /since=([^&]+)/.exec(path);
  if (m && m[1] === server.sig) return Promise.resolve({ unchanged: true, sig: server.sig });
  return Promise.resolve(JSON.parse(JSON.stringify(server)));
}
eval(SECTION);
var bad = [];
function ses(sig, st, turns) { var l = [{ index: 1, status: "succeeded", answer: "a1" }, { index: 2, status: st, answer: "" }]; return { id: "chat_1", sig: sig, status: "running", turns: turns, turn_max: 50, turn_list: l }; }
(async function () {
  server = ses("s1", "running", 1);
  var c = bindChat(sec, { id: "chat_1", key: "k", pre: JSON.parse(JSON.stringify(server)) });
  var r0 = renders;
  await c.load();
  if (requests[0] !== "/api/chat-sessions/chat_1?since=s1") bad.push("poll after preload: " + requests[0]);
  if (renders !== r0) bad.push("unchanged poll rendered " + (renders - r0) + " turns");
  if (capCalls < 2) bad.push("unchanged poll skipped the capability cards");
  await c.load(); await c.load();
  if (renders !== r0) bad.push("three unchanged polls rendered " + (renders - r0) + " turns");
  // the answer arrives: one full reply, repainted, the next poll carries the new sig
  server = ses("s2", "succeeded", 2); server.turn_list[1].answer = "a2"; server.status = "idle";
  await c.load();
  if (requests[requests.length - 1] !== "/api/chat-sessions/chat_1?since=s1" || renders === r0) bad.push("changed session not repainted");
  await c.load();
  if (requests[requests.length - 1] !== "/api/chat-sessions/chat_1?since=s2") bad.push("sig not updated: " + requests[requests.length - 1]);
  // gone: the sig is forgotten
  status = 404; await c.load(); status = 200;
  var before = requests.length; await c.load();
  if (requests.length !== before) bad.push("load after 404 still asked: " + requests.slice(before));
  // a session without a preload asks in full first
  requests = []; var c2 = bindChat(sec, { id: "chat_1", key: "k2" }); await null; await null;
  if (requests[0] !== "/api/chat-sessions/chat_1") bad.push("first load sent since: " + requests[0]);
  if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
})().catch(function (e) { console.error(e); process.exit(1); });
`, map[string]string{"SECTION": js[start:end]})
}
