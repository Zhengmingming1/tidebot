package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestConnectionBar: when tidebot can't be reached every engine's own English error ("Failed
// to fetch", "Load failed", "NetworkError…") becomes one translated message, a short blip shows
// nothing, a lasting outage shows one bar that retries with backoff and a countdown, a click
// while down toasts once, and the page repaints when the server answers again.
func TestConnectionBar(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`return netFetch(path, { method: method, headers: headers, body: body, credentials: "same-origin", signal: ac ? ac.signal : undefined })`,
		`return netFetch(path, { credentials: "same-origin" }).then(function (res) {`,
		`return netFetch("/api/desktop/save?name=" + enc(name),`,
		`return netFetch(path, { method: "POST", headers: { "Accept": "application/json", "X-CSRF-Token": state.csrf }, body: fd`,
		`if (net.down) return Promise.resolve(); // the reconnect probe is already asking`,
		"if (ended) return;\n        netCheck();",
		"if (e && e.offline) return;\n      showLogin();",
		`window.addEventListener("online", netNow);`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	// Only the i18n loader, netFetch and its probe call fetch() directly.
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "fetch(") && !strings.Contains(line, "netFetch(") && !strings.Contains(line, "refetch") && !strings.Contains(line, "prefetch") && !strings.Contains(line, "Prefetch") &&
			!strings.Contains(line, `fetch("/static/i18n/`) && !strings.Contains(line, "return fetch(path, init)") && !strings.Contains(line, `fetch("/api/rev", { credentials: "same-origin", cache: "no-store"`) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
			t.Errorf("raw fetch() outside netFetch: %s", strings.TrimSpace(line))
		}
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".net-bar {", "body.net-on .toasts", "@media (prefers-reduced-motion: reduce) { .net-bar { animation: none; } }"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lost %q", want)
		}
	}
	keys := []string{"err.offline", "conn.lost", "conn.retry_in", "conn.retrying", "conn.now", "conn.back", "conn.tip"}
	for _, lang := range []string{"en", "zh-CN"} {
		raw, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]string
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if strings.TrimSpace(d[k]) == "" {
				t.Errorf("%s.json: missing %s", lang, k)
			}
		}
		if !strings.Contains(d["conn.retry_in"], "{n}") || !strings.Contains(d["conn.tip"], "{t}") || !strings.Contains(d["conn.tip"], "{n}") {
			t.Errorf("%s.json: conn placeholders", lang)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	blk := cut(t, src, "  // ---------- connection ----------", "  // CSP forbids inline style attributes")
	failFn := cut(t, src, "  // A click that failed because tidebot is unreachable", "  // ---------- connection ----------")
	escFn := cut(t, src, "  function esc(v) {", "\n  }\n") + "\n  }\n"
	script := `
var bad = [];
function eq(got, want, what) { if (got !== want) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
// fake clock
var now = 0, timers = [], seq = 0;
function setTimeout(fn, ms) { var id = ++seq; timers.push({ id: id, at: now + (ms || 0), fn: fn }); return id; }
function clearTimeout(id) { timers = timers.filter(function (x) { return x.id !== id; }); }
var intervals = [];
function setInterval(fn, ms) { var id = ++seq; intervals.push({ id: id, ms: ms, next: now + ms, fn: fn }); return id; }
function clearInterval(id) { intervals = intervals.filter(function (x) { return x.id !== id; }); }
function advance(ms) {
  var end = now + ms;
  for (;;) {
    var t = timers.slice().sort(function (a, b) { return a.at - b.at; })[0];
    var iv = intervals.slice().sort(function (a, b) { return a.next - b.next; })[0];
    var nt = t ? t.at : Infinity, ni = iv ? iv.next : Infinity;
    if (Math.min(nt, ni) > end) break;
    if (nt <= ni) { now = nt; clearTimeout(t.id); t.fn(); } else { now = ni; iv.next += iv.ms; iv.fn(); }
  }
  now = end;
}
function flush() { return new Promise(function (r) { setImmediate(r); }); }
// tiny DOM
function El(tag) { this.tagName = tag; this.attrs = {}; this.children = []; this.className = ""; this.innerHTML = ""; this.removed = false; var self = this;
  this.classList = { contains: function (c) { return (" " + self.className + " ").indexOf(" " + c + " ") >= 0; }, add: function (c) { if (!this.contains(c)) self.className = (self.className + " " + c).trim(); }, remove: function (c) { self.className = (" " + self.className + " ").replace(" " + c + " ", " ").trim(); } }; }
El.prototype.setAttribute = function (k, v) { this.attrs[k] = String(v); };
El.prototype.removeAttribute = function (k) { delete this.attrs[k]; };
El.prototype.remove = function () { this.removed = true; document.body.children = document.body.children.filter(function (x) { return x !== this; }, this); };
El.prototype.addEventListener = function () {};
var listeners = {};
var document = { hidden: false, activeElement: null, body: new El("BODY"), createElement: function (t) { return new El(t.toUpperCase()); },
  getElementById: function (id) { return id === "boot" && bootSplash ? {} : null; }, addEventListener: function (ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); } };
document.body.appendChild = function (el) { this.children.push(el); };
var window = { AbortController: null, addEventListener: function (ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); } };
var bootSplash = false, modalOpen = false, errPage = false;
function $(sel, root) {
  if (sel === "#net-bar") return document.body.children.filter(function (x) { return x.id === "net-bar"; })[0] || null;
  if (sel === "#nb-now") return root ? (root._btn = root._btn || { clicks: 0, addEventListener: function () { this.clicks++; }, focus: function () {} }) : null;
  if (sel === ".nb-sub") return root ? (root._sub = root._sub || { textContent: "" }) : null;
  if (sel === "#modal-root") return { innerHTML: modalOpen ? "<div>" : "" };
  if (sel === "#err-retry") return errPage ? {} : null;
  return null;
}
function page() { return null; }
var state = { csrf: "tok", lang: "en" }, lastHash = "#/";
var DICT = { "err.offline": "OFFLINE", "conn.lost": "LOST", "conn.retry_in": "in {n}", "conn.retrying": "RETRYING", "conn.now": "NOW", "conn.back": "BACK", "conn.tip": "{t} {n}" };
function T(k, v) { var s = DICT[k] || k; if (v) s = s.replace(/\{(\w+)\}/g, function (m, n) { return v[n] == null ? m : String(v[n]); }); return s; }
function ic(n) { return "<i " + n + ">"; }
function num(n) { return String(n); }
function clock() { return "12:00"; }
var log = [];
function boot() { log.push("boot"); }
function loadCC() { log.push("cc"); }
function api(p) { log.push("api " + p); return Promise.resolve({}); }
function noteNotifs() {}
function route() { log.push("route " + lastHash); }
function refresh() { log.push("refresh"); }
var toasts = [];
function toast(m, bad) { var t = { m: m, open: true }; toasts.push(t); return function () { t.open = false; }; }
// fake fetch: server up or down; proxy mode answers 502 text/html
var server = "up", probes = 0;
function Res(status, ct) { this.status = status; this.ok = status < 400; this.headers = { get: function () { return ct; } }; }
function fetch(path) {
  if (path === "/api/rev") probes++;
  if (server === "down") return Promise.reject(new TypeError("Failed to fetch"));
  if (server === "proxy") return Promise.resolve(new Res(502, "text/html"));
  return Promise.resolve(new Res(200, "application/json"));
}
` + escFn + failFn + blk + `
function bar() { return $("#net-bar"); }
(async function () {
  // classification across engines
  eq(netLost(new TypeError("Failed to fetch")), true, "chrome");
  eq(netLost(new TypeError("Load failed")), true, "safari");
  eq(netLost(new TypeError("NetworkError when attempting to fetch resource.")), true, "firefox");
  var ab = new Error("aborted"); ab.name = "AbortError";
  eq(netLost(ab), false, "our timeout");
  eq(netLost(new SyntaxError("Unexpected token")), false, "bad json");
  eq(netLost(null, new Res(502, "text/html")), true, "proxy 502");
  eq(netLost(null, new Res(503, "text/html; charset=utf-8")), true, "proxy 503");
  eq(netLost(null, new Res(503, "application/json")), false, "tidebot 503 json");
  eq(netLost(null, new Res(500, "text/plain")), false, "500");
  eq(netLost(null, new Res(401, "application/json")), false, "401");
  eq([0, 1, 2, 7, 8, 50].map(netDelay).join(","), "2,3,5,30,30,30", "backoff");

  // blip: one failed request, server back before the probe → nothing shown, no refresh
  server = "down";
  var e1 = null; await netFetch("/api/tasks").catch(function (e) { e1 = e; });
  eq(e1 && e1.offline, true, "offline flag"); eq(e1 && e1.message, "OFFLINE", "translated");
  eq(net.down, true, "down");
  server = "up"; advance(1500); await flush(); await flush();
  eq(net.down, false, "blip recovered"); eq(bar(), null, "no bar for a blip"); eq(log.length, 0, "no refresh for a blip");

  // lasting outage → bar after the first failed probe, countdown, backoff
  server = "down";
  await netFetch("/api/x").catch(function () {});
  advance(1500); await flush(); await flush();
  eq(!!bar(), true, "bar shown"); eq(probes, 2, "probes so far");
  eq(/LOST/.test(bar().innerHTML) && bar()._sub.textContent, "in 2", "countdown 2");
  var built = bar().innerHTML, btn = bar()._btn;
  eq(document.body.classList.contains("net-on"), true, "toasts move up");
  eq(bar().attrs["data-tip"], "12:00 1", "time and attempts in the tooltip");
  eq(bar().attrs.role, "status", "announced politely");
  advance(1000); await flush();
  eq(bar()._sub.textContent, "in 1", "countdown 1");
  eq(bar().innerHTML === built && bar()._btn === btn && btn.clicks === 1, true, "bar built once, only the countdown changes");
  advance(1000); await flush(); await flush();
  eq(probes, 3, "second probe at 2s");
  eq(bar()._sub.textContent, "in 3", "then 3s");
  // hidden tab: countdown stands still, no probes
  document.hidden = true; advance(60000); await flush();
  eq(probes, 3, "no probes while hidden");
  document.hidden = false;
  // a click while down toasts once, not per click
  fail(netError()); fail(netError()); fail(netError());
  eq(toasts.filter(function (t) { return t.open; }).length, 1, "one offline toast");
  fail(new Error("other")); eq(toasts.length, 2, "other errors still toast");
  // a request answered by a proxy 502 page still counts as down
  server = "proxy"; netNow(); await flush(); await flush();
  eq(net.down, true, "proxy page = down"); eq(probes, 4, "retry now probes");
  // reconnect: probe answers → back bar, toast closed, page refreshed, bar gone after 2s
  server = "up"; netNow(); await flush(); await flush();
  eq(net.down, false, "up again");
  eq(bar() && bar().classList.contains("back"), true, "reconnected bar");
  eq(toasts[0].open, false, "offline toast closed");
  eq(log.indexOf("refresh") >= 0 && log.indexOf("cc") >= 0, true, "page repainted: " + log.join("|"));
  advance(2000);
  eq(bar(), null, "bar hides"); eq(document.body.classList.contains("net-on"), false, "toasts back");

  // with a dialog open the page is not repainted; an error page is re-routed; boot retried
  log = []; modalOpen = true; server = "down"; await netFetch("/a").catch(function () {}); advance(1500); await flush(); await flush();
  server = "up"; netNow(); await flush(); await flush();
  eq(log.indexOf("refresh"), -1, "modal keeps the page: " + log.join("|")); modalOpen = false;
  log = []; errPage = true; server = "down"; await netFetch("/a").catch(function () {}); advance(1500); await flush(); await flush();
  server = "up"; netNow(); await flush(); await flush();
  eq(log.indexOf("route null") >= 0, true, "error page re-routed: " + log.join("|")); errPage = false;
  log = []; bootSplash = true; server = "down"; await netFetch("/api/me").catch(function () {}); advance(1500); await flush(); await flush();
  server = "up"; window.online = true; listeners.online[0](); await flush(); await flush();
  eq(log.join("|"), "boot", "boot retried after a failed start"); bootSplash = false;
  console.log(JSON.stringify(bad));
})();
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var bad []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &bad); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	for _, s := range bad {
		t.Error(s)
	}
}
