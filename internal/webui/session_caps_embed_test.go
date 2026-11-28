package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeProposalCaps answers "proposals" like the app: {unchanged} for a matching since.
type fakeProposalCaps struct {
	sig   string
	err   error
	calls []CapabilityRequest
}

func (f *fakeProposalCaps) CapabilityRequest(_ context.Context, action string, r CapabilityRequest) (any, error) {
	if action != "proposals" {
		return nil, errors.New("unexpected action " + action)
	}
	f.calls = append(f.calls, r)
	if f.err != nil {
		return nil, f.err
	}
	if r.Since != "" && r.Since == f.sig {
		return map[string]any{"unchanged": true, "sig": f.sig}, nil
	}
	return map[string]any{"proposals": []map[string]string{{"id": "p1", "status": "pending", "name": "<b>x</b>"}}, "sig": f.sig}, nil
}

// TestChatSessionEmbedsProposals: GET /api/chat-sessions/{id}?caps=1 carries the session's
// extension proposals exactly as GET /api/capabilities/proposals would answer, on the full
// answer and on the short {unchanged} one, so the session page's poll is one request, not two.
func TestChatSessionEmbedsProposals(t *testing.T) {
	c := login(t)
	f := &fakeSignedChats{sig: "s1"}
	caps := &fakeProposalCaps{sig: "p-a"}
	c.s.deps.ChatSessions = f
	c.s.deps.Capabilities = caps
	cp := func(out map[string]any) map[string]any { m, _ := out["cap_proposals"].(map[string]any); return m }

	// without caps=1 nothing changes and the capability API is not asked
	for _, q := range []string{"", "?since=s1", "?caps=0", "?cap_since=p-a"} {
		code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1"+q, nil)
		if code != 200 || out["cap_proposals"] != nil {
			t.Fatalf("%q: %d %v", q, code, out)
		}
	}
	if len(caps.calls) != 0 {
		t.Fatalf("asked without caps=1: %v", caps.calls)
	}
	// full session: the whole list, same body as the proposals endpoint
	code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?caps=1", nil)
	if code != 200 || out["turn_list"] == nil || cp(out) == nil || cp(out)["sig"] != "p-a" {
		t.Fatalf("full: %d %v", code, out)
	}
	if len(caps.calls) != 1 || caps.calls[0].SessionID != "chat_1" || caps.calls[0].Since != "" {
		t.Fatalf("request %+v", caps.calls)
	}
	_, direct := c.json(http.MethodGet, "/api/capabilities/proposals?session_id=chat_1", nil)
	a, _ := json.Marshal(cp(out))
	b, _ := json.Marshal(direct)
	if string(a) != string(b) {
		t.Fatalf("embedded differs:\n%s\n%s", a, b)
	}
	if strings.Contains(c.do(http.MethodGet, "/api/chat-sessions/chat_1?caps=1", "", nil).Body.String(), "<b>") {
		t.Fatal("proposal text not escaped in JSON")
	}
	// unchanged session + unchanged proposals: both short
	rr := c.do(http.MethodGet, "/api/chat-sessions/chat_1?since=s1&caps=1&cap_since=p-a", "", nil)
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != `{"cap_proposals":{"sig":"p-a","unchanged":true},"sig":"s1","unchanged":true}` || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unchanged: %d %s", rr.Code, rr.Body)
	}
	// unchanged session, changed proposals: the session stays short, the list comes whole
	caps.sig = "p-b"
	code, out = c.json(http.MethodGet, "/api/chat-sessions/chat_1?since=s1&caps=1&cap_since=p-a", nil)
	if code != 200 || out["unchanged"] != true || cp(out)["sig"] != "p-b" || cp(out)["proposals"] == nil {
		t.Fatalf("changed list: %d %v", code, out)
	}
	if f.gets != 5 {
		t.Fatalf("turns read on an unchanged session: gets=%d", f.gets)
	}
	// a failing capability API leaves the field out (the page asks itself), the session still answers
	caps.err = errors.New("boom")
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?caps=1", nil); code != 200 || out["cap_proposals"] != nil || out["turn_list"] == nil {
		t.Fatalf("failing caps: %d %v", code, out)
	}
	// no capability API: no field
	c.s.deps.Capabilities = nil
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?caps=1", nil); code != 200 || out["cap_proposals"] != nil {
		t.Fatalf("unwired: %d %v", code, out)
	}
	// a missing session is 404 before anything is asked
	c.s.deps.Capabilities = caps
	caps.err, caps.calls = nil, nil
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_x?caps=1", nil); code != 404 || len(caps.calls) != 0 {
		t.Fatalf("missing: %d calls=%d", code, len(caps.calls))
	}
}

// TestSessionProposalsEmbeddedUI runs capSessionQuery / capRefreshSession under node: a list
// that came with the session is painted without a request, an {unchanged} one for the list we
// have sends nothing, and anything we can't use (stale since, a request of our own in flight,
// a forced refresh, a failed box) still asks as before.
func TestSessionProposalsEmbeddedUI(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function capPaintProposals(")
	end := strings.Index(js, "\n  function capPropose(")
	if start < 0 || end < start {
		t.Fatal("session proposal functions not found")
	}
	for _, want := range []string{
		`api("/api/chat-sessions/" + enc(id) + "?caps=1&drafts=1", { fresh: true })`,
		`api("/api/chat-sessions/" + enc(cs.id) + "?caps=1", { fresh: true })`,
		`capSessionQuery(sec)`,
		`capRefreshSession(sec, id, false, s.cap_proposals)`,
		`delete s.cap_proposals`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	runNode(t, `
function El(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.parent = null; }
El.prototype.getAttribute = function (k) { return this.attrs[k] == null ? null : this.attrs[k]; };
El.prototype.appendChild = function (c) { if (c.parent) c.parent.removeChild(c); c.parent = this; this.children.push(c); return c; };
El.prototype.removeChild = function (c) { this.children.splice(this.children.indexOf(c), 1); c.parent = null; };
El.prototype.replaceChild = function (n, o) { if (n.parent) n.parent.removeChild(n); this.children[this.children.indexOf(o)] = n; n.parent = this; o.parent = null; };
El.prototype.insertBefore = function (n, ref) { if (n.parent) n.parent.removeChild(n); var i = ref ? this.children.indexOf(ref) : this.children.length; this.children.splice(i, 0, n); n.parent = this; };
El.prototype.remove = function () { if (this.parent) this.parent.removeChild(this); };
Object.defineProperty(El.prototype, "firstChild", { get: function () { return this.children[0] || null; } });
Object.defineProperty(El.prototype, "innerHTML", { set: function (h) { var self = this; this.children = []; h.replace(/<article data-cap-proposal="([^"]+)">([^<]*)<\/article>/g, function (_, id, text) { var e = new El("article"); e.attrs["data-cap-proposal"] = id; e.text = text; self.appendChild(e); }); } });
Object.defineProperty(El.prototype, "textContent", { set: function (v) { this.children = []; this.text = v; } });
var document = { createElement: function (t) { return new El(t); }, contains: function () { return true; } };
var sec = new El("section"), form = new El("form"); form.attrs.id = "rc-form"; sec.appendChild(form);
function $(sel, root) { if (sel === ".cap-session-proposals") return root.children.filter(function (c) { return c.className === sel.slice(1); })[0] || null; if (sel === "#rc-form") return form; return null; }
function $$(sel, root) { return root.children.filter(function (c) { return c.getAttribute("data-cap-proposal") != null; }); }
function enc(s) { return encodeURIComponent(s); } function T(k) { return k; }
var requests = [], server = null, now = 1000000, hold = null;
Date.now = function () { return now; };
function setTimeout() { return 0; } function clearTimeout() {}
function capWireProposals() {}
function capProposalHtml(p) { return '<article data-cap-proposal="' + p.id + '">' + p.id + ':' + p.status + '</article>'; }
function api(path) { requests.push(path); if (hold) return hold; var r = JSON.parse(JSON.stringify(server)); var m = /since=([^&]+)/.exec(path); if (m && m[1] === r.sig) r = { unchanged: true, sig: r.sig }; return Promise.resolve(r); }
eval(SECTION);
var bad = [];
function box() { return $(".cap-session-proposals", sec); }
function ids() { return box().children.map(function (c) { return c.getAttribute("data-cap-proposal"); }).join(); }
async function settle() { for (var i = 0; i < 6; i++) await null; }
(async function () {
  // before anything is drawn: no since
  if (capSessionQuery(sec) !== "caps=1") bad.push("first query " + capSessionQuery(sec));
  // the list that came with the session: painted, no request
  server = { sig: "s1", proposals: [{ id: "a", status: "pending" }] };
  capRefreshSession(sec, "c1", false, { sig: "s1", proposals: [{ id: "a", status: "pending" }] }); await settle();
  if (requests.length || ids() !== "a") bad.push("embedded paint: " + requests + " " + ids());
  if (capSessionQuery(sec) !== "caps=1&cap_since=s1") bad.push("query " + capSessionQuery(sec));
  var a = box().children[0];
  // unchanged for the list we have: nothing asked, nothing redrawn (even past the throttle)
  now += 5000; capRefreshSession(sec, "c1", false, { unchanged: true, sig: "s1" }); await settle();
  if (requests.length || box().children[0] !== a) bad.push("unchanged: " + requests);
  // throttle counts from the embedded answer: a plain refresh right after sends nothing
  capRefreshSession(sec, "c1"); await settle();
  if (requests.length) bad.push("not throttled after embedded: " + requests);
  // a new list rides along: only it is painted, a keeps its node
  capRefreshSession(sec, "c1", false, { sig: "s2", proposals: [{ id: "b", status: "pending" }, { id: "a", status: "pending" }] }); await settle();
  if (requests.length || ids() !== "b,a" || box().children[1] !== a) bad.push("new list: " + requests + " " + ids());
  // {unchanged} for a list we don't have (asked with an older since): ask ourselves
  server = { sig: "s3", proposals: [{ id: "c", status: "pending" }] };
  now += 5000; capRefreshSession(sec, "c1", false, { unchanged: true, sig: "s9" }); await settle();
  if (requests.length !== 1 || !/since=s2/.test(requests[0]) || ids() !== "c") bad.push("stale unchanged: " + requests + " " + ids());
  // a forced refresh (after confirm / dismiss) always asks
  requests = []; now += 5000;
  capRefreshSession(sec, "c1", true, { sig: "s3", proposals: [] }); await settle();
  if (requests.length !== 1) bad.push("forced did not ask: " + requests);
  // a request of our own is out: the embedded list is not used, the answer follows
  requests = []; now += 5000; var release; hold = new Promise(function (r) { release = r; });
  capRefreshSession(sec, "c1", true); 
  capRefreshSession(sec, "c1", false, { sig: "s0", proposals: [{ id: "old", status: "pending" }] });
  if (ids() === "old") bad.push("embedded used while busy");
  hold = null; release({ sig: "s4", proposals: [{ id: "d", status: "pending" }] }); await settle();
  if (ids() !== "d") bad.push("busy answer: " + ids());
  // a failed box: query asks for the whole list, an embedded list clears the error
  requests = []; now += 5000; hold = Promise.reject({ status: 500 }); hold.catch(function () {});
  capRefreshSession(sec, "c1", true); await settle(); hold = null;
  if (box().text !== "cap.load_error" || !box()._failed) bad.push("failure not shown");
  if (capSessionQuery(sec) !== "caps=1") bad.push("failed query " + capSessionQuery(sec));
  capRefreshSession(sec, "c1", false, { sig: "s5", proposals: [{ id: "e", status: "pending" }] }); await settle();
  if (box()._failed || ids() !== "e") bad.push("recovery: " + ids());
  if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
})().catch(function (e) { console.error(e); process.exit(1); });
`, map[string]string{"SECTION": js[start:end]})
}

// TestRunDetailChatEmbedsProposals: the session a run starts comes with GET
// /api/runs/{id}?chat=1, and now its proposal cards too (the run page shows them).
func TestRunDetailChatEmbedsProposals(t *testing.T) {
	c := login(t)
	c.s.deps.ChatSessions = &fakeChats{}
	caps := &fakeProposalCaps{sig: "p-a"}
	c.s.deps.Capabilities = caps
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_o','succeeded','p',?)`,
		`INSERT INTO chat_sessions(id,origin_run_id,created_at,updated_at) VALUES('chat_1','run_o',?,?)`,
		`INSERT INTO chat_session_turns(session_id,turn_index,run_id,created_at) VALUES('chat_1',0,'run_o',?)`,
	} {
		args := []any{now}
		if strings.Count(q, "?") == 2 {
			args = append(args, now)
		}
		if _, err := c.s.deps.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	code, out := c.json(http.MethodGet, "/api/runs/run_o?chat=1", nil)
	chat, _ := out["chat"].(map[string]any)
	if code != 200 || chat == nil {
		t.Fatalf("run: %d %v", code, out)
	}
	cp, _ := chat["cap_proposals"].(map[string]any)
	if cp == nil || cp["sig"] != "p-a" || cp["proposals"] == nil || len(caps.calls) != 1 || caps.calls[0].SessionID != "chat_1" || caps.calls[0].Since != "" {
		t.Fatalf("cap_proposals %v calls %+v", chat["cap_proposals"], caps.calls)
	}
	// without chat=1 nothing is read
	caps.calls = nil
	if _, out := c.json(http.MethodGet, "/api/runs/run_o", nil); out["chat"] != nil || len(caps.calls) != 0 {
		t.Fatalf("plain run asked: %v %d", out["chat"], len(caps.calls))
	}
}
