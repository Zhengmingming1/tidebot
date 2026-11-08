package webui

import (
	"strings"
	"testing"
)

// TestDeviceLoginDialogRecoverable: the Extensions "Sign in to DingTalk" dialog used to
// disable Start for good after a click, reuse install-card status copy ("Installation
// failed" / raw English words), and let an in-flight status GET schedule a second poll
// after Start or Cancel. Start works again after fail/cancel, each action keeps one
// poll chain, and login statuses have their own strings.
func TestDeviceLoginDialogRecoverable(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  function capLoginLabel(")
	end := strings.Index(js, "\n  function capStorageBytes(")
	if start < 0 || end < start {
		t.Fatal("device login functions not found")
	}
	if !strings.Contains(js[start:end], "function () { stop(); }") {
		t.Fatal("dialog does not stop polling when the modal closes")
	}
	runNode(t, `
function El(tag) {
  this.tag = tag; this.children = []; this.listeners = {}; this.parent = null;
  this.disabled = false; this.text = ""; this.className = ""; this.href = ""; this.target = ""; this.rel = "";
}
El.prototype.addEventListener = function (ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); };
El.prototype.click = function () {
  var self = this;
  (this.listeners.click || []).forEach(function (fn) { fn({ currentTarget: self }); });
};
El.prototype.appendChild = function (c) { c.parent = this; this.children.push(c); return c; };
Object.defineProperty(El.prototype, "textContent", {
  get: function () { return this.text + this.children.map(function (c) { return c.textContent; }).join(""); },
  set: function (v) { this.children = []; this.text = String(v); }
});
function $(sel, root) {
  if (sel === "#cap-device-state") return output;
  if (sel === "#cap-device-start") return startBtn;
  if (sel === "#cap-device-cancel") return cancelBtn;
  if (sel === "#cap-device-error") return errEl;
  return null;
}
function T(k) { return k; }
function enc(s) { return encodeURIComponent(s); }
function esc(s) { return String(s); }
var document = { createElement: function (t) { return new El(t); }, contains: function () { return open; } };
var output = new El("div"), startBtn = new El("button"), cancelBtn = new El("button"), errEl = new El("p");
var root = new El("div"), open = true;
var requests = [], timers = [], mounted = null;
function modal(html, onMount, onClose) { mounted = { html: html, onMount: onMount, onClose: onClose }; }
function setTimeout(fn, ms) { timers.push({ fn: fn, ms: ms, cleared: false }); return timers.length; }
function clearTimeout(id) { if (id && timers[id - 1]) timers[id - 1].cleared = true; }
function liveTimers() { return timers.filter(function (t) { return t && !t.cleared; }); }
function api(path, opts) {
  var rec = { path: path, opts: opts || {} };
  rec.promise = new Promise(function (res, rej) { rec.resolve = res; rec.reject = rej; });
  requests.push(rec);
  return rec.promise;
}
eval(SECTION);
var bad = [];
function ok(c, m) { if (!c) bad.push(m); }
function last() { return requests[requests.length - 1]; }
function pendingGet() { return requests.filter(function (r) { return !r.opts.method && !r.settled; }); }
function dump() {
  return "reqs=" + JSON.stringify(requests.map(function (r) { return { path: r.path, method: r.opts.method || "GET", settled: !!r.settled }; })) + " liveTimers=" + liveTimers().length;
}
function resolveLast(v) {
  var r = last();
  if (!r || !r.resolve) throw new Error("no last request to resolve; " + dump());
  r.settled = true; r.resolve(v);
}
function resolveGet(v) {
  var r = pendingGet()[0];
  if (!r) throw new Error("no pending GET; " + dump());
  r.settled = true; r.resolve(v);
}
function fireTimer() {
  var t = liveTimers()[0];
  if (!t) throw new Error("no live timer; " + dump());
  t.cleared = true; t.fn();
}
(async function () {
  var stop = capBindDeviceLogin(root, "/api/capabilities/demo/login");
  await null;
  ok(requests.length === 1 && requests[0].path === "/api/capabilities/demo/login" && !requests[0].opts.method, "open sends one GET");
  resolveGet({ status: "idle" });
  await null; await null;
  ok(output.textContent.indexOf("cap.login_idle") >= 0, "idle uses login copy: " + output.text);
  ok(!startBtn.disabled && cancelBtn.disabled, "idle: start on, cancel off");
  ok(liveTimers().length === 0, "idle does not poll");

  // Start while the first refresh is still in flight: that GET must not add a second timer.
  open = true;
  output = new El("div"); startBtn = new El("button"); cancelBtn = new El("button"); errEl = new El("p");
  requests = []; timers = [];
  stop = capBindDeviceLogin(root, "/api/capabilities/demo/login");
  await null;
  var stale = requests[0];
  startBtn.click();
  await null;
  ok(startBtn.disabled, "start disables immediately");
  ok(last().opts.method === "POST" && last().opts.body && last().opts.body.reviewed === true, "start POSTs reviewed");
  resolveLast({ status: "starting" });
  await null; await null;
  ok(startBtn.disabled && !cancelBtn.disabled, "starting: start off, cancel on");
  ok(output.textContent.indexOf("cap.login_starting") >= 0, "starting label");
  ok(liveTimers().length === 1, "one timer after start, got " + liveTimers().length);
  stale.settled = true; stale.resolve({ status: "starting" });
  await null; await null;
  ok(liveTimers().length === 1, "stale GET after start must not add a second timer, got " + liveTimers().length);

  fireTimer();
  await null;
  ok(pendingGet().length === 1, "timer sends one GET");
  resolveGet({ status: "pending", user_code: "<img src=x>", verification_uri: "https://login.dingtalk.com/oauth/device" });
  await null; await null;
  ok(output.textContent.indexOf("cap.login_pending") >= 0 && output.textContent.indexOf("<img src=x>") >= 0, "pending shows code as text");
  ok(output.children.every(function (c) { return c.tag !== "img"; }), "user_code is not parsed as HTML");
  var link = output.children.filter(function (c) { return c.tag === "a"; })[0];
  ok(link && /^https:\/\/login\.dingtalk\.com\/oauth\/device\/?$/.test(link.href) && link.target === "_blank" && link.rel === "noopener noreferrer", "https link: " + (link && link.href));
  ok(liveTimers().length === 1, "pending keeps one timer");

  // A non-https URI must not become a link.
  fireTimer();
  await null;
  resolveGet({ status: "pending", user_code: "ABCD", verification_uri: "http://evil.example/login" });
  await null; await null;
  ok(output.children.filter(function (c) { return c.tag === "a"; }).length === 0, "http URI ignored");

  // Failed login: Start works again, no more polling, dedicated copy (not cap.failed).
  fireTimer();
  await null;
  resolveGet({ status: "failed" });
  await null; await null;
  ok(!startBtn.disabled && cancelBtn.disabled, "failed: start on, cancel off");
  ok(output.textContent.indexOf("cap.login_failed") >= 0 && output.textContent.indexOf("cap.login_failed_note") >= 0, "failed copy: " + output.textContent);
  ok(output.textContent.indexOf("cap.failed") < 0, "must not reuse install-failed copy");
  ok(liveTimers().length === 0, "failed stops polling");

  // Retry after failure, then cancel while a GET is in flight.
  startBtn.click();
  await null;
  resolveLast({ status: "pending", user_code: "ZZ", verification_uri: "https://login.dingtalk.com/ok" });
  await null; await null;
  ok(liveTimers().length === 1, "retry pending has one timer");
  fireTimer();
  await null;
  var inflight = pendingGet()[0];
  cancelBtn.click();
  await null;
  ok(last().opts.method === "POST" && /\/cancel$/.test(last().path), "cancel POSTs");
  inflight.settled = true; inflight.resolve({ status: "pending", user_code: "ZZ", verification_uri: "https://login.dingtalk.com/ok" });
  await null; await null;
  ok(liveTimers().length === 0, "stale GET after cancel must not reschedule, got " + liveTimers().length);
  resolveLast({ status: "cancelled" });
  await null; await null;
  ok(!startBtn.disabled && cancelBtn.disabled, "cancelled: start on again");
  ok(output.textContent.indexOf("cap.login_cancelled") >= 0, "cancelled label");
  ok(liveTimers().length === 0, "cancelled does not poll");

  startBtn.click();
  await null;
  resolveLast({ status: "completed" });
  await null; await null;
  ok(!startBtn.disabled, "completed: start on so the user can sign in again");
  ok(output.textContent.indexOf("cap.login_completed") >= 0 && output.textContent.indexOf("cap.login_completed_note") >= 0, "completed copy");
  ok(liveTimers().length === 0, "completed does not poll");

  // POST error re-enables Start and refreshes status (covers 409 already-pending).
  output = new El("div"); startBtn = new El("button"); cancelBtn = new El("button"); errEl = new El("p");
  requests = []; timers = [];
  stop = capBindDeviceLogin(root, "/api/capabilities/demo/login");
  await null;
  resolveLast({ status: "idle" });
  await null; await null;
  startBtn.click();
  await null;
  last().settled = true; last().reject(new Error("already pending"));
  await null; await null;
  ok(errEl.text === "already pending", "error text: " + errEl.text);
  ok(!startBtn.disabled, "POST error re-enables start until status returns");
  ok(pendingGet().length === 1, "POST error refreshes status");
  resolveGet({ status: "pending", user_code: "XY" });
  await null; await null;
  ok(startBtn.disabled && !cancelBtn.disabled, "409 refresh shows pending and disables start");

  // Closing the dialog drops late replies and clears the timer.
  var n = requests.length;
  stop();
  ok(liveTimers().length === 0, "close clears timer");
  startBtn.click();
  await null;
  ok(requests.length === n, "close ignores further clicks");

  mounted = null;
  capDeviceLoginDialog("cap/<x>");
  ok(mounted.html.indexOf('id="cap-device-start"') >= 0 && mounted.html.indexOf('role="alert"') >= 0, "dialog markup");
  ok(mounted.html.indexOf("window.confirm") < 0 && typeof mounted.onClose === "function", "in-app dialog with close hook");
  var called = 0, bound = "";
  var prev = capBindDeviceLogin;
  capBindDeviceLogin = function (r, base) { bound = base; return function () { called++; }; };
  mounted.onMount(root);
  ok(bound === "/api/capabilities/cap%2F%3Cx%3E/login", "capability id encoded: " + bound);
  mounted.onClose();
  ok(called === 1, "closing the modal stops the binder");
  capBindDeviceLogin = prev;

  if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`, map[string]string{"SECTION": js[start:end]})
}
