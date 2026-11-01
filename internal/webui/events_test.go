package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

type evStream struct {
	t      *testing.T
	msgs   chan evState
	cancel context.CancelFunc
	done   chan struct{}
}

func openEvents(t *testing.T, url, sid string) (*evStream, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/events", nil)
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sid})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		cancel()
		return nil, res.StatusCode
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") || res.Header.Get("Content-Encoding") != "" {
		t.Errorf("headers %q %q", ct, res.Header.Get("Content-Encoding"))
	}
	es := &evStream{t: t, msgs: make(chan evState, 64), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(es.done)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var st evState
				if json.Unmarshal([]byte(d), &st) == nil {
					es.msgs <- st
				}
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-es.done })
	return es, 200
}

func (es *evStream) next(within time.Duration) (evState, bool) {
	select {
	case st := <-es.msgs:
		return st, true
	case <-time.After(within):
		return evState{}, false
	}
}

// wait returns the first message satisfying ok.
func (es *evStream) wait(what string, ok func(evState) bool) evState {
	es.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, got := es.next(time.Until(deadline)); got && ok(st) {
			return st
		}
	}
	es.t.Fatalf("no event: %s", what)
	return evState{}
}

func shortEvents(t *testing.T, tick, beat time.Duration) {
	ot, ob := eventsTick, eventsHeartbeat
	eventsTick, eventsHeartbeat = tick, beat
	t.Cleanup(func() { eventsTick, eventsHeartbeat = ot, ob })
}

// TestEventsStream: the first message is the state; reads send nothing; a new notification
// moves notif and rev, a run starting moves busy, the read mark moves notif; the watcher stops
// with the last stream.
func TestEventsStream(t *testing.T) {
	shortEvents(t, 20*time.Millisecond, time.Hour)
	c := login(t)
	c.json(http.MethodGet, "/api/me", nil) // may write last_seen_at once
	ts := httptest.NewServer(c.s.routes())
	t.Cleanup(ts.Close)
	es, code := openEvents(t, ts.URL, c.sid)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	first, ok := es.next(3 * time.Second)
	if !ok || first.Rev == "" || first.Notif == "" || first.Busy != "0" {
		t.Fatalf("first %+v %v", first, ok)
	}
	for _, p := range []string{"/api/overview", "/api/runs", "/api/notifications/center", "/api/rev"} {
		c.do(http.MethodGet, p, "", nil)
	}
	if st, got := es.next(300 * time.Millisecond); got && st.Rev != first.Rev {
		t.Fatalf("reads sent %+v", st)
	}
	db := c.s.deps.DB
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO notifications(id,event,body,created_at,msg_key,msg_params) VALUES('n1','run_failed','x',?,'','')`, now); err != nil {
		t.Fatal(err)
	}
	st := es.wait("notification", func(s evState) bool { return s.Notif != first.Notif })
	if st.Rev == first.Rev || st.Busy != "0" {
		t.Fatalf("after notification %+v", st)
	}
	if _, err := db.Exec(`INSERT INTO runs(id,status,prompt,created_at) VALUES('r1','running','p',?)`, now); err != nil {
		t.Fatal(err)
	}
	st2 := es.wait("busy", func(s evState) bool { return s.Busy == "1" })
	if st2.Notif != st.Notif {
		t.Fatalf("a run moved the bell %+v", st2)
	}
	if code, out := c.json(http.MethodPost, "/api/notifications/read", map[string]string{"seen_at": now}); code != 200 {
		t.Fatalf("read %d %v", code, out)
	}
	es.wait("read mark", func(s evState) bool { return s.Notif != st2.Notif })
	es.cancel()
	<-es.done
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.s.events.mu.Lock()
		idle := c.s.events.stop == nil && len(c.s.events.subs) == 0
		c.s.events.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher still running with no stream open")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEventsAuthAndCap: no session → 401; past eventsMax → 429 (that page keeps polling);
// a session signed out elsewhere ends its stream at the next heartbeat.
func TestEventsAuthAndCap(t *testing.T) {
	shortEvents(t, 20*time.Millisecond, 50*time.Millisecond)
	c := login(t)
	ts := httptest.NewServer(c.s.routes())
	t.Cleanup(ts.Close)
	if _, code := openEvents(t, ts.URL, ""); code != 401 {
		t.Fatalf("anon %d", code)
	}
	var open []*evStream
	for i := 0; i < eventsMax; i++ {
		es, code := openEvents(t, ts.URL, c.sid)
		if code != 200 {
			t.Fatalf("stream %d: %d", i, code)
		}
		open = append(open, es)
	}
	if _, code := openEvents(t, ts.URL, c.sid); code != 429 {
		t.Fatalf("over cap %d", code)
	}
	for _, es := range open[1:] {
		es.cancel()
		<-es.done
	}
	// heartbeats repeat the state, so a page can tell the stream is alive
	es := open[0]
	es.next(time.Second)
	if _, ok := es.next(time.Second); !ok {
		t.Fatal("no heartbeat")
	}
	if _, err := c.s.deps.DB.Exec(`DELETE FROM sessions WHERE id=?`, c.sid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-es.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream outlived its session")
	}
}

// TestEventsShutdown: stopping the WebUI ends open streams instead of waiting them out.
func TestEventsShutdown(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	if err := c.s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c.s.mu.Lock()
	addr := c.s.ln.Addr().String()
	c.s.mu.Unlock()
	es, code := openEvents(t, "http://"+addr, c.sid)
	if code != 200 {
		c.s.Stop(ctx)
		t.Fatalf("status %d", code)
	}
	es.next(2 * time.Second)
	start := time.Now()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.s.Stop(sctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop waited %v", d)
	}
	select {
	case <-es.done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream still open after stop")
	}
}

// TestEventsClient: the page side under node, two tabs of one browser. Only the tab holding
// the lock opens a stream, the other gets each message over the BroadcastChannel; a moved
// notif / busy token refreshes the bell / status; a refused stream backs off; signing out
// hands the lock to the other tab; a silent stream counts as down after a minute.
func TestEventsClient(t *testing.T) {
	src := appJS(t)
	start := strings.Index(src, "  // ---------- change stream (/api/events) ----------")
	end := strings.Index(src, "  // ---------- login ----------")
	if start < 0 || end < start {
		t.Fatal("change stream section not found")
	}
	for _, want := range []string{
		"if (evsLive()) return; // the change stream refreshes the bell",
		"(!evsLive() || Date.now() - cc.at > 120000)) loadCC(false);",
		`(evsLive() ? Promise.resolve({ rev: evs.st.rev }) : apiRaw("/api/rev"))`,
		"return busy || evsLive() ? 2 :",
		"    stopStream();\n    evsStop();\n",
		"      route();\n      evsStart();\n",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	runNode(t, `
var vm = require("vm");
var bus = [], lockQ = [], held = null, sources = [], timers = [];
function BC(name){ this.name=name; bus.push(this); }
BC.prototype.postMessage = function(d){ var me=this; bus.forEach(function(b){ if(b!==me && b.onmessage) b.onmessage({data: JSON.parse(JSON.stringify(d))}); }); };
var locks = { request: function(name, fn){ return new Promise(function(done){ lockQ.push({fn:fn, done:done}); grant(); }); } };
function grant(){
  if (held || !lockQ.length) return;
  held = lockQ.shift();
  Promise.resolve(held.fn()).then(function(){ var h=held; held=null; h.done(); grant(); });
}
function ES(url){ this.url=url; this.readyState=1; sources.push(this); }
ES.prototype.close = function(){ this.readyState=2; this.closed=true; };
function tab(name){
  var c = { name:name, calls:[], ccLoads:0 };
  c.ctx = vm.createContext({
    window: { EventSource: ES, BroadcastChannel: BC }, EventSource: ES, BroadcastChannel: BC,
    navigator: { locks: locks }, document: { hidden:false, addEventListener: function(){} },
    setInterval: function(){}, clearTimeout: function(t){ if(t) t.off=true; },
    setTimeout: function(f, ms){ var t={f:f, ms:ms}; timers.push(t); return t; },
    state: { csrf: "x" }, cc: { at: 5 }, Promise: Promise, JSON: JSON, Math: Math, Date: Date,
    api: function(p, o){ c.calls.push(p + (o && o.fresh ? "!" : "")); return Promise.resolve({}); },
    noteNotifs: function(){}, loadCC: function(){ c.ccLoads++; c.ctxCCat = c.ctx.cc.at; },
  });
  vm.runInContext(SECTION, c.ctx);
  c.run = function(code){ return vm.runInContext(code, c.ctx); };
  return c;
}
var tick = function(){ return new Promise(function(r){ setImmediate(r); }); };
var bad = [];
(async function(){
  var a = tab("a"), b = tab("b");
  a.run("evsStart()"); await tick(); b.run("evsStart()"); await tick();
  if (sources.length !== 1) bad.push("streams opened: " + sources.length);
  if (!a.run("evs.leader") || b.run("evs.leader")) bad.push("leader");
  var es = sources[0];
  es.onopen();
  es.onmessage({ data: JSON.stringify({ rev:"1.1", notif:"n1", busy:"0" }) });
  if (!a.run("evsLive()") || !b.run("evsLive()")) bad.push("not live after first message");
  if (a.calls.length || b.calls.length || a.ccLoads || b.ccLoads) bad.push("first message refreshed");
  es.onmessage({ data: JSON.stringify({ rev:"2.1", notif:"n1", busy:"0" }) });
  if (a.calls.length || b.calls.length) bad.push("rev alone refreshed the bell");
  if (b.run("evs.st.rev") !== "2.1") bad.push("follower rev");
  es.onmessage({ data: JSON.stringify({ rev:"3.1", notif:"n2", busy:"0" }) });
  if (a.calls.join() !== "/api/notifications/center!" || b.calls.join() !== "/api/notifications/center!") bad.push("bell: " + a.calls + " / " + b.calls);
  es.onmessage({ data: JSON.stringify({ rev:"4.1", notif:"n2", busy:"1" }) });
  if (a.ccLoads !== 1 || b.ccLoads !== 1 || a.ctxCCat !== 0) bad.push("status refresh " + a.ccLoads + " " + b.ccLoads + " " + a.ctxCCat);
  es.onmessage({ data: "not json" });
  // refused for good: both tabs fall back to polling, the leader retries after 1 s, then 2 s
  es.readyState = 2; es.onerror();
  if (a.run("evsLive()") || b.run("evsLive()")) bad.push("still live after error");
  var t1 = timers[timers.length-1]; if (!t1 || t1.ms !== 1000) bad.push("first retry " + (t1 && t1.ms));
  t1.f(); var es2 = sources[sources.length-1]; if (es2 === es) bad.push("no reconnect");
  es2.readyState = 2; es2.onerror();
  if (timers[timers.length-1].ms !== 2000) bad.push("backoff " + timers[timers.length-1].ms);
  timers[timers.length-1].f(); var es3 = sources[sources.length-1];
  es3.onopen(); es3.onmessage({ data: JSON.stringify({ rev:"5.1", notif:"n2", busy:"1" }) });
  if (!b.run("evsLive()")) bad.push("follower after reconnect");
  // silent for a minute (machine slept): down, and the leader reconnects
  a.run("evs.at = Date.now() - 61000"); if (a.run("evsLive()")) bad.push("stale counted live");
  var n = sources.length; a.run("evsCheck()"); if (sources.length !== n + 1 || !es3.closed) bad.push("stale stream not replaced");
  // sign out in tab a: the stream closes and tab b takes over
  a.run("evsStop()"); await tick(); await tick();
  var es4 = sources[sources.length-1];
  if (!sources[n].closed) bad.push("signed-out stream left open");
  if (!b.run("evs.leader") || es4 === sources[n]) bad.push("lock not handed on");
  if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
})();
`, map[string]string{"SECTION": src[start:end]})
}

func appJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
}

// runNode runs script under node with each vars entry defined as a JS string constant.
func runNode(t *testing.T, script string, vars map[string]string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	pre := ""
	for k, v := range vars {
		b, _ := json.Marshal(v)
		pre += "var " + k + " = " + string(b) + ";\n"
	}
	if out, err := exec.Command(node, "-e", pre+script).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

// TestEventsDesktop: the desktop window's in-process handler (its own session, no cookie
// from the page) streams the same way.
func TestEventsDesktop(t *testing.T) {
	shortEvents(t, 20*time.Millisecond, time.Hour)
	c := login(t)
	h, err := c.s.DesktopHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	es, code := openEvents(t, ts.URL, "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	first, ok := es.next(3 * time.Second)
	if !ok || first.Rev == "" {
		t.Fatalf("first %+v", first)
	}
	if _, err := c.s.deps.DB.Exec(`INSERT INTO runs(id,status,prompt,created_at) VALUES('r1','queued','p',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	es.wait("busy", func(s evState) bool { return s.Busy == "1" })
}
