package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fakeSignedDrafts: a session with a fingerprint, skill drafts and a status to set.
type fakeSignedDrafts struct {
	fakeDrafts
	sig    string
	status string
	gets   int
}

func (f *fakeSignedDrafts) ChatSessionSig(_ context.Context, id string) (string, error) {
	if id != "chat_1" {
		return "", ErrChatSessionNotFound
	}
	return f.sig, nil
}
func (f *fakeSignedDrafts) GetChatSession(ctx context.Context, id string) (ChatSessionDetail, error) {
	f.gets++
	d, err := f.fakeDrafts.GetChatSession(ctx, id)
	d.Status = f.status
	return d, err
}

// TestChatSessionEmbedsDrafts: GET /api/chat-sessions/{id}?drafts=1 carries the session's
// skill drafts exactly as GET …/draft answers, but only on a full answer for a session that
// is not answering: the 1.5 s poll while a turn runs (and every {unchanged} reply) never
// reads the drafts.
func TestChatSessionEmbedsDrafts(t *testing.T) {
	c := login(t)
	f := &fakeSignedDrafts{sig: "s1", status: "idle"}
	c.s.deps.ChatSessions = f
	gets := func() int {
		n := 0
		for _, x := range f.calls {
			if x == "get" {
				n++
			}
		}
		return n
	}
	// not asked: no field, the drafts are not read
	for _, q := range []string{"", "?caps=1", "?drafts=0", "?drafts=true"} {
		code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1"+q, nil)
		if code != 200 || out["draft"] != nil || out["turn_list"] == nil {
			t.Fatalf("%q: %d %v", q, code, out)
		}
	}
	if gets() != 0 {
		t.Fatalf("drafts read without drafts=1: %v", f.calls)
	}
	// idle session: the same body as the draft endpoint
	code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?caps=1&drafts=1", nil)
	if code != 200 || out["turn_list"] == nil || out["draft"] == nil || gets() != 1 {
		t.Fatalf("idle: %d %v calls=%v", code, out, f.calls)
	}
	_, direct := c.json(http.MethodGet, "/api/chat-sessions/chat_1/draft", nil)
	a, _ := json.Marshal(out["draft"])
	b, _ := json.Marshal(direct)
	if string(a) != string(b) {
		t.Fatalf("embedded differs:\n%s\n%s", a, b)
	}
	if strings.Contains(c.do(http.MethodGet, "/api/chat-sessions/chat_1?drafts=1", "", nil).Body.String(), "<b>") {
		t.Fatal("skill name not escaped in JSON")
	}
	// unchanged session: short reply, drafts not read
	f.calls = nil
	rr := c.do(http.MethodGet, "/api/chat-sessions/chat_1?since=s1&drafts=1", "", nil)
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != `{"sig":"s1","unchanged":true}` || gets() != 0 {
		t.Fatalf("unchanged: %d %s calls=%v", rr.Code, rr.Body, f.calls)
	}
	// a turn is being answered: the poll stays as light as before
	f.status = "running"
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?drafts=1", nil); code != 200 || out["draft"] != nil || gets() != 0 || out["status"] != "running" {
		t.Fatalf("running: %d %v calls=%v", code, out, f.calls)
	}
	// the answer that shows the turn finished brings the drafts it left
	f.status, f.sig = "idle", "s2"
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?since=s1&drafts=1", nil); code != 200 || out["draft"] == nil || out["sig"] != "s2" || gets() != 1 {
		t.Fatalf("finished: %d %v", code, out)
	}
	// failing drafts: the session still answers, without the field (the page asks itself)
	f.derr = errors.New("boom")
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?drafts=1", nil); code != 200 || out["draft"] != nil || out["turn_list"] == nil {
		t.Fatalf("failing: %d %v", code, out)
	}
	// an app without drafts: no field
	c.s.deps.ChatSessions = &fakeSignedChats{sig: "s1"}
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_1?drafts=1", nil); code != 200 || out["draft"] != nil {
		t.Fatalf("no drafter: %d %v", code, out)
	}
	// a missing session is 404 before the drafts are read
	c.s.deps.ChatSessions = f
	f.derr, f.calls = nil, nil
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_x?drafts=1", nil); code != 404 || gets() != 0 {
		t.Fatalf("missing: %d calls=%v", code, f.calls)
	}
}

// TestSessionDraftsEmbeddedUI runs csDraftBar under node: drafts that came with the session
// are painted without a request, a finished turn's answer that carries them sends nothing,
// and anything else (none embedded, a session opened mid-turn) still asks as before.
func TestSessionDraftsEmbeddedUI(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function csDraftList(")
	end := strings.Index(js, "\n  function draftStale(")
	if start < 0 || end < start {
		t.Fatal("draft bar functions not found")
	}
	for _, want := range []string{
		`api("/api/chat-sessions/" + enc(id) + "?caps=1&drafts=1", { fresh: true })`,
		`query: function () { return "drafts=1"; }`,
		`o.query ? o.query() : ""`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	runNode(t, `
function El(id) { this.id = id; this.hidden = true; this.html = ""; this.on = {}; }
El.prototype.addEventListener = function (k, f) { this.on[k] = f; };
Object.defineProperty(El.prototype, "innerHTML", { set: function (h) { this.html = h; this.paints = (this.paints || 0) + 1; }, get: function () { return this.html; } });
function $(sel) { return els[sel] || null; }
function T(k) { return k; } function esc(s) { return String(s); } function ic() { return ""; } function enc(s) { return s; }
function draftStale() { return false; } function toast() {} function fail() {}
var requests = [], answer = null, els;
function api(path) { requests.push(path); return Promise.resolve(JSON.parse(JSON.stringify(answer))); }
eval(SECTION);
var bad = [];
async function settle() { for (var i = 0; i < 6; i++) await null; }
function fresh() { els = { "#cs-draft": new El("cs-draft"), "#cs-tune": new El("cs-tune") }; requests = []; }
var one = { drafts: [{ skill_id: "k1", skill_name: "A", base_no: 1 }], skills: [{ id: "k1" }] };
var two = { drafts: [{ skill_id: "k1", skill_name: "A", base_no: 1 }, { skill_id: "k2", skill_name: "B", base_no: 3 }], skills: [{ id: "k1" }, { id: "k2" }] };
(async function () {
  // opened idle with the drafts: painted at once, nothing asked, used once
  fresh();
  var ses = { id: "c1", status: "idle", draft: one };
  var bar = csDraftBar({}, ses); await settle();
  if (requests.length || els["#cs-draft"].hidden || !/data-skill="k1"/.test(els["#cs-draft"].html)) bad.push("open embedded: " + requests + " " + els["#cs-draft"].html);
  if ("draft" in ses) bad.push("embedded draft not dropped");
  // a turn runs (no drafts with the poll), then finishes with them: no request, new rows
  bar.session({ id: "c1", status: "running" }); await settle();
  if (els["#cs-draft"].html.indexOf("disabled") < 0) bad.push("buttons not off while running");
  bar.session({ id: "c1", status: "idle", draft: two }); await settle();
  if (requests.length || !/data-skill="k2"/.test(els["#cs-draft"].html)) bad.push("finished embedded: " + requests);
  // finished without drafts in the answer (older server): asks as before
  answer = one;
  bar.session({ id: "c1", status: "running" }); bar.session({ id: "c1", status: "idle" }); await settle();
  if (requests.length !== 1 || requests[0] !== "/api/chat-sessions/c1/draft" || /data-skill="k2"/.test(els["#cs-draft"].html)) bad.push("finished plain: " + requests);
  // opened without drafts (mid-turn, older server): asks once
  fresh(); answer = one;
  bar = csDraftBar({}, { id: "c2", status: "running" }); await settle();
  if (requests.length !== 1 || !/data-skill="k1"/.test(els["#cs-draft"].html)) bad.push("open plain: " + requests);
  // opened mid-turn: the first idle answer with drafts is used without asking
  requests = [];
  bar.session({ id: "c2", status: "idle", draft: two }); await settle();
  if (requests.length || !/data-skill="k2"/.test(els["#cs-draft"].html)) bad.push("mid-turn finish: " + requests);
  // an unchanged repaint does not touch the DOM
  var n = els["#cs-draft"].paints;
  bar.session({ id: "c2", status: "idle" }); await settle();
  if (els["#cs-draft"].paints !== n || requests.length) bad.push("repaint churn");
  if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
})().catch(function (e) { console.error(e); process.exit(1); });
`, map[string]string{"SECTION": js[start:end]})
}
