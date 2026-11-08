package webui

import (
	"strings"
	"testing"
)

// TestSessionProposalCardsKeepInput: while a turn is answered the chat page refreshes the
// session's install cards every poll. It used to download the whole list (payloads up to
// 1 MB each) every time and, on any change, rebuild every card, wiping the boxes the user had
// ticked. Now an unchanged list is a short {unchanged} reply, only the card that changed is
// replaced, and a card is repainted as expired when its time is up, without a request.
func TestSessionProposalCardsKeepInput(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function capPaintProposals(")
	end := strings.Index(js, "\n  function capPropose(")
	if start < 0 || end < start {
		t.Fatal("session proposal functions not found")
	}
	runNode(t, `
// tiny DOM: elements with attributes, children, and innerHTML that parses top-level <article>s
function El(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.parent = null; this.checked = false; }
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
function enc(s) { return s; } function T(k) { return k; }
var wired = 0, requests = [], server = null, timers = [], now = 1000000;
Date.now = function () { return now; };
function setTimeout(fn, ms) { timers.push({ fn: fn, at: now + ms }); return timers.length; }
function clearTimeout(id) { if (id) timers[id - 1] = null; }
function capWireProposals(root) { wired += root.children.length; }
function capProposalHtml(p) { var exp = p.status === "pending" && Date.parse(p.expires_at) <= Date.now(); return '<article data-cap-proposal="' + p.id + '">' + p.id + ':' + (exp ? "expired" : p.status) + '</article>'; }
function api(path) { requests.push(path); var r = JSON.parse(JSON.stringify(server)); var m = /since=([^&]+)/.exec(path); if (m && m[1] === r.sig) r = { unchanged: true, sig: r.sig }; return Promise.resolve(r); }
eval(SECTION);
var bad = [];
function cards() { return $(".cap-session-proposals", sec).children; }
function card(id) { return cards().filter(function (c) { return c.getAttribute("data-cap-proposal") === id; })[0]; }
function iso(ms) { return new Date(ms).toISOString(); }
var origParse = Date.parse;
(async function () {
  server = { sig: "s1", proposals: [{ id: "a", status: "pending", expires_at: iso(now + 60000) }, { id: "b", status: "pending", expires_at: iso(now + 120000) }] };
  capRefreshSession(sec, "chat1", true); await null; await null; await null;
  if (cards().length !== 2 || wired !== 2) bad.push("first paint: cards " + cards().length + " wired " + wired);
  if (requests[0].indexOf("since=") >= 0) bad.push("first request sent since");
  card("a").checked = true; var a = card("a"), b = card("b");
  // unchanged: the request carries the sig, the reply is short, no card touched
  now += 1500; capRefreshSession(sec, "chat1"); await null; await null; await null;
  if (!/since=s1/.test(requests[1] || "")) bad.push("poll without since: " + requests[1]);
  if (card("a") !== a || card("b") !== b || wired !== 2) bad.push("unchanged list repainted");
  // a new card arrives on top: a and b keep their DOM (and the ticked box)
  server = { sig: "s2", proposals: [{ id: "c", status: "pending", expires_at: iso(now + 90000) }, server.proposals[0], server.proposals[1]] };
  now += 1500; capRefreshSession(sec, "chat1"); await null; await null; await null;
  if (cards().map(function (c) { return c.getAttribute("data-cap-proposal"); }).join() !== "c,a,b") bad.push("order: " + cards().map(function (c) { return c.getAttribute("data-cap-proposal"); }).join());
  if (card("a") !== a || !card("a").checked || card("b") !== b) bad.push("new card rebuilt the others");
  if (wired !== 3) bad.push("wired " + wired + ", want only the new card");
  // b is dismissed: only b is replaced
  server = JSON.parse(JSON.stringify(server)); server.sig = "s3"; server.proposals[2].status = "dismissed";
  now += 1500; capRefreshSession(sec, "chat1"); await null; await null; await null;
  if (card("b") === b || card("b").text !== "b:dismissed" || card("a") !== a) bad.push("status change: b " + card("b").text);
  // throttled: a second poll right away sends nothing
  var n = requests.length; capRefreshSession(sec, "chat1"); if (requests.length !== n) bad.push("not throttled");
  // a's time runs out: the timer repaints a as expired with no request
  var due = timers.filter(Boolean).reduce(function (m, x) { return !m || x.at < m.at ? x : m; }, null);
  if (!due) bad.push("no expiry timer");
  else { now = due.at; due.fn(); if (card("a").text !== "a:expired" || requests.length !== n) bad.push("expiry repaint: " + card("a").text + " requests " + (requests.length - n)); }
  // a card gone from the list is removed
  server = { sig: "s4", proposals: [server.proposals[0]] };
  now += 1500; capRefreshSession(sec, "chat1", true); await null; await null; await null;
  if (cards().length !== 1 || !card("c")) bad.push("removed card left: " + cards().length);
  if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
})().catch(function (e) { console.error(e); process.exit(1); });
`, map[string]string{"SECTION": js[start:end]})
}
