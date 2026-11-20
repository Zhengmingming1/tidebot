package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestChatUndoCheckUI: session turn cards reuse GET /api/runs/{id}/undo-check
// (the run page's API) before the in-app confirm, mark files edited since, and
// turn the button off when the whole action would be refused. The 1.5 s poll
// does not start those checks; hover / focus / click do.
func TestChatUndoCheckUI(t *testing.T) {
	b, err := os.ReadFile("src/run-chat.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	logb, err := os.ReadFile("src/log-view.js")
	if err != nil {
		t.Fatal(err)
	}
	logSrc := strings.ReplaceAll(string(logb), "\r\n", "\n")
	app := appJS(t)
	for _, s := range []string{
		`api("/api/runs/" + enc(id) + "/undo-check", { fresh: true })`,
		`thread.addEventListener("pointerover", prefetch);`,
		`thread.addEventListener("focusin", prefetch);`,
		`okDisabled: !!blocked`,
		`o.typeName || o.okDisabled ? " disabled"`,
	} {
		if !strings.Contains(app, s) && !strings.Contains(src, s) && !strings.Contains(logSrc, s) {
			t.Errorf("bundle/src lacks %s", s)
		}
	}
	if regexp.MustCompile(`\b(window\.)?(confirm|alert|prompt)\(`).MatchString(src) {
		t.Error("run-chat.js uses a native dialog")
	}
	css := cssSource(t)
	for _, s := range []string{".icon-btn.ck-off", ".rc-a-head .rd-chg-stale"} {
		if !strings.Contains(css, s) {
			t.Errorf("styles lack %s", s)
		}
	}

	fn := regexp.MustCompile(`(?s)  var UNDO_CK_CODES = \{ dir: 1, busy: 1, binary: 1 \};\n  // Whether the main undo.*?\n  function undoCkSet\(b, off, tip\) \{\n.*?\n  function applyUndoCheck`).FindString(logSrc)
	if fn == "" {
		t.Fatal("undo check helpers not found in log-view.js")
	}
	fn = strings.TrimSuffix(fn, "  function applyUndoCheck")
	turnFn := regexp.MustCompile(`(?s)  var RC_UNDO_CK_TTL = 30000, rcUndoCk = \{\}, rcUndoCkWait = \{\}, rcUndoBusy = \{\};\n.*?\n  function rcTurnChgHtml`).FindString(src)
	if turnFn == "" {
		t.Fatal("session undo-check functions not found")
	}
	turnFn = strings.TrimSuffix(turnFn, "  function rcTurnChgHtml")

	runNode(t, `
function El(tag, attrs) { this.tagName = tag; this.attrs = attrs || {}; this.children = []; this.parent = null; this.textContent = ""; this.disabled = false; this._in = true; var self = this;
  this.classList = { add: function (c) { var s = (self.attrs["class"] || "").split(" ").filter(Boolean); if (s.indexOf(c) < 0) s.push(c); self.attrs["class"] = s.join(" "); },
    remove: function (c) { self.attrs["class"] = (self.attrs["class"] || "").split(" ").filter(function (x) { return x && x !== c; }).join(" "); },
    contains: function (c) { return (self.attrs["class"] || "").split(" ").indexOf(c) >= 0; } }; }
El.prototype.getAttribute = function (k) { return k in this.attrs ? this.attrs[k] : null; };
El.prototype.setAttribute = function (k, v) { this.attrs[k] = String(v); };
El.prototype.hasAttribute = function (k) { return k in this.attrs; };
El.prototype.removeAttribute = function (k) { delete this.attrs[k]; };
El.prototype.insertBefore = function (n, ref) { n.parent = this; var i = this.children.indexOf(ref); if (i < 0) this.children.push(n); else this.children.splice(i, 0, n); };
El.prototype.remove = function () { var p = this.parent; if (p) p.children.splice(p.children.indexOf(this), 1); this.parent = null; };
El.prototype.set = function (k, v) { this.attrs[k] = v; return this; };
Object.defineProperty(El.prototype, "className", { set: function (v) { this.attrs["class"] = v; }, get: function () { return this.attrs["class"] || ""; } });
Object.defineProperty(El.prototype, "parentNode", { get: function () { return this.parent; } });
function add(p, c) { c.parent = p; p.children.push(c); return c; }
function walk(e, f, out) { (e.children || []).forEach(function (c) { if (f(c)) out.push(c); walk(c, f, out); }); return out; }
function match(sel) {
  if (sel === "[data-rc-undo]") return function (e) { return e.hasAttribute("data-rc-undo"); };
  if (sel === ".rd-chg-stale") return function (e) { return e.classList.contains("rd-chg-stale"); };
  throw new Error("selector " + sel);
}
function $(sel, root) { return walk(root, match(sel), [])[0] || null; }
var document = { createElement: function (t) { return new El(t); }, contains: function (n) { return !!(n && n._in); } };
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function enc(s) { return encodeURIComponent(s); }
function num(n) { return String(n); }
(function () {
`+fn+`
`+turnFn+`
var fail = function (m) { console.error(m); process.exitCode = 1; };
var calls = [], confs = [], toasts = [];
var apiRes = { whole: "conflict", whole_files: ["a.txt", "<b>.txt"] };
function api(url, o) { calls.push({ url: url, o: o }); return api.fail ? Promise.reject({ message: "net" }) : Promise.resolve(apiRes); }
function confirmDialog(o) { confs.push(o); return Promise.resolve(false); }
function toast(m, err) { toasts.push({ m: m, err: !!err }); }
function runUndoChanges(r, redo, btn, ck) { return confirmDialog({ title: "rd.undo_title", body: "rd.undo_body" + (undoCheckBlocks(ck) ? "\n\n" + undoCheckWarn(ck, false) : ""), ok: "rd.chg_undo", okDisabled: !!undoCheckBlocks(ck) }); }

var head = new El("div").set("class", "rc-a-head");
var btn = add(head, new El("button").set("data-rc-undo", "undo").set("data-tip", "orig").set("class", "icon-btn sm"));
applyTurnUndoCheck(btn, { whole: "conflict", whole_files: ["<b>.txt"], files: { "<b>.txt": { ok: false, detail: "error: <b>.txt: patch does not apply" } } });
if (btn.getAttribute("aria-disabled") !== "true" || btn.getAttribute("data-tip").indexOf("rd.undo_ck_whole_f") !== 0 || btn.getAttribute("data-tip").indexOf("<b>.txt") < 0 || !btn.classList.contains("ck-off")) fail("button off " + JSON.stringify(btn.attrs));
var mk = $(".rd-chg-stale", head);
if (!mk || mk.textContent !== "rd.chg_stale" || mk.getAttribute("data-tip").indexOf("<b>.txt") < 0 || head.children.indexOf(mk) > head.children.indexOf(btn)) fail("stale mark");
applyTurnUndoCheck(btn, { whole: "conflict", whole_files: ["<b>.txt"] });
if (walk(head, match(".rd-chg-stale"), []).length !== 1) fail("mark doubled");
applyTurnUndoCheck(btn, { whole: "ok", files: { "a.txt": { ok: true } } });
if (btn.getAttribute("aria-disabled") || btn.getAttribute("data-tip") !== "orig" || $(".rd-chg-stale", head) || btn.classList.contains("ck-off")) fail("not back on");
applyTurnUndoCheck(btn, { code: "busy", files: {} });
if (btn.getAttribute("data-tip") !== "rd.undo_err_busy" || btn.getAttribute("aria-disabled") !== "true" || $(".rd-chg-stale", head)) fail("busy");
applyTurnUndoCheck(btn, { code: "none", files: {} });
if (btn.getAttribute("aria-disabled")) fail("none code turned the button off");
btn.disabled = true; btn.removeAttribute("aria-disabled");
applyTurnUndoCheck(btn, { whole: "conflict", files: {} });
if (btn.getAttribute("aria-disabled")) fail("already disabled button touched");
btn.disabled = false;

var turn = { index: 1, run_id: "run_e", changes: { files: 2, add: 3, del: 1 } };
return rcUndoTurn(turn, "/tmp/ws", false, btn).then(function () {
  if (calls.length !== 1 || calls[0].url !== "/api/runs/run_e/undo-check" || !calls[0].o || !calls[0].o.fresh) fail("click did not ask undo-check " + JSON.stringify(calls));
  if (confs.length !== 1 || !confs[0].okDisabled || (confs[0].body || "").indexOf("rd.undo_ck_whole_f") < 0 || (confs[0].body || "").indexOf("<b>.txt") < 0) fail("blocked confirm " + JSON.stringify(confs[0]));
  if (btn.getAttribute("aria-disabled") !== "true") fail("click result not applied to button");
  return rcUndoTurn(turn, "/tmp/ws", false, btn);
}).then(function () {
  if (calls.length !== 2) fail("click must ask again, got " + calls.length);
  confs = []; toasts = [];
  turn = { index: 2, run_id: "run_f", changes: { files: 1, undone: true } };
  btn.setAttribute("data-rc-undo", "redo"); btn.removeAttribute("aria-disabled"); btn.classList.remove("ck-off");
  apiRes = { whole: "conflict", whole_files: ["z.txt"] };
  return rcUndoTurn(turn, "/tmp/ws", true, btn);
}).then(function () {
  if (confs.length) fail("redo conflict opened confirm");
  if (toasts.length !== 1 || String(toasts[0].m).indexOf("rd.undo_ck_redo_f") < 0 || !toasts[0].err) fail("redo conflict toast " + JSON.stringify(toasts));
  confs = [];
  api.fail = true;
  return rcUndoTurn({ index: 3, run_id: "run_g", changes: { files: 1 } }, "/tmp/ws", false, btn);
}).then(function () {
  if (confs.length !== 1 || confs[0].okDisabled) fail("fallback confirm " + JSON.stringify(confs[0]));
  var n = calls.length;
  rcUndoCk["run_e"] = { at: Date.now(), x: { whole: "conflict", whole_files: ["a.txt"] } };
  var art = new El("article").set("data-turn", "1");
  var h = add(art, new El("div").set("class", "rc-a-head"));
  var b2 = add(h, new El("button").set("data-rc-undo", "undo").set("data-tip", "orig").set("class", "icon-btn sm"));
  rcRestoreUndoCheck(art, { run_id: "run_e" });
  if (b2.getAttribute("aria-disabled") !== "true" || !($(".rd-chg-stale", h))) fail("restore from cache");
  if (calls.length !== n) fail("restore must not fetch");
}).catch(function (e) { fail(e && e.stack || e); });
})();
`, nil)
}
