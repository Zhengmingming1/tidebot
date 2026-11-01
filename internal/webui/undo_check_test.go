package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestUndoCheckAPI: GET /api/runs/{id}/undo-check hands the app's answer over as is,
// never cached; an unknown run is a 404; signed out it is refused.
func TestUndoCheckAPI(t *testing.T) {
	c := login(t)
	seedRun(t, c, "uc1", "p", "succeeded", 0, time.Now())
	c.s.deps.CheckRunUndo = func(ctx context.Context, id string) (UndoCheck, error) {
		if id != "uc1" {
			return UndoCheck{}, ErrRunNotFound
		}
		return UndoCheck{Whole: "conflict", WholeFiles: []string{"b.txt"}, Files: map[string]UndoFileCheck{"a.txt": {OK: true}, "b.txt": {Detail: "error: b.txt: patch does not apply"}}}, nil
	}
	rec := c.do(http.MethodGet, "/api/runs/uc1/undo-check", "", nil)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var out UndoCheck
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Whole != "conflict" || !out.Files["a.txt"].OK || out.Files["b.txt"].OK || out.Files["b.txt"].Detail == "" {
		t.Fatalf("%s %v", rec.Body.String(), err)
	}
	if rec = c.do(http.MethodGet, "/api/runs/nope/undo-check", "", nil); rec.Code != 404 {
		t.Fatalf("unknown run %d", rec.Code)
	}
	c.s.deps.CheckRunUndo = func(ctx context.Context, id string) (UndoCheck, error) { return UndoCheck{Code: "none"}, nil }
	if rec = c.do(http.MethodGet, "/api/runs/uc1/undo-check", "", nil); !strings.Contains(rec.Body.String(), `"code":"none"`) || !strings.Contains(rec.Body.String(), `"files":{}`) {
		t.Fatalf("code only: %s", rec.Body.String())
	}
	anon := &client{t: t, s: c.s}
	if rec = anon.do(http.MethodGet, "/api/runs/uc1/undo-check", "", nil); rec.Code != 401 {
		t.Fatalf("signed out %d", rec.Code)
	}
}

// TestUndoCheckUI: the card asks when drawn and when the window comes back; a file
// edited since gets an escaped "edited since" mark and its button is turned off (not
// removed) with the reason, the main button too; a later clean answer turns them back
// on with the original tooltips; dir / busy turn everything off; no native dialogs.
func TestUndoCheckUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`api("/api/runs/" + enc(r.id) + "/undo-check", { fresh: true })`,
		`window.addEventListener("focus", ask);`,
		`document.addEventListener("visibilitychange", ask);`,
		`watchUndoCheck(card, r);`,
		`if (b.getAttribute("aria-disabled") === "true") return;`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".btn.ck-off", ".rd-chg-fx.ck-off", ".rd-chg-stale"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles lack %s", s)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for k, ph := range map[string]string{"rd.undo_ck_whole": "", "rd.undo_ck_whole_f": "{f}", "rd.undo_ck_redo": "", "rd.undo_ck_redo_f": "{f}", "rd.undo_ck_f": "", "rd.undo_ck_f_redo": "", "rd.chg_stale": "", "rd.chg_stale_tip": ""} {
			if m[k] == "" || !strings.Contains(m[k], ph) {
				t.Errorf("%s %s missing or lacks %q", f, k, ph)
			}
		}
	}
	fn := regexp.MustCompile(`(?s)  function watchUndoCheck\(card, r\) \{\n.*?\n  function chgDiffHtml`).FindString(src)
	if fn == "" {
		t.Fatal("functions not found")
	}
	if regexp.MustCompile(`\b(confirm|alert|prompt)\(`).MatchString(fn) {
		t.Error("undo check uses a native dialog")
	}
	fn = strings.TrimSuffix(fn, "  function chgDiffHtml")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `
// tiny DOM, enough for applyUndoCheck
function El(tag, attrs) { this.tagName = tag; this.attrs = attrs || {}; this.children = []; this.parent = null; this.textContent = ""; this.disabled = false; var self = this;
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
function add(p, c) { c.parent = p; p.children.push(c); return c; }
function walk(e, f, out) { e.children.forEach(function (c) { if (f(c)) out.push(c); walk(c, f, out); }); return out; }
function match(sel) {
  if (sel === "[data-chg-undo]") return function (e) { return e.hasAttribute("data-chg-undo"); };
  if (sel === "[data-chg-file]") return function (e) { return e.hasAttribute("data-chg-file"); };
  if (sel === "details[data-chg]") return function (e) { return e.tagName === "details"; };
  if (sel === "summary") return function (e) { return e.tagName === "summary"; };
  if (sel === ".grow") return function (e) { return e.classList.contains("grow"); };
  if (sel === ".rd-chg-stale") return function (e) { return e.classList.contains("rd-chg-stale"); };
  throw new Error("selector " + sel);
}
function $(sel, root) { return walk(root, match(sel), [])[0] || null; }
function $$(sel, root) { return walk(root, match(sel), []); }
var document = { createElement: function (t) { return new El(t); } };
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function enc(s) { return encodeURIComponent(s); }
` + fn + `
var fail = function (m) { console.error(m); process.exitCode = 1; };
var card = new El("section");
var ub = add(card, new El("button").set("data-chg-undo", "undo").set("data-tip", "orig"));
var rows = ["a.txt", "<b>.txt"].map(function (p, i) {
  var d = add(card, new El("details").set("data-chg", String(i))), sm = add(d, new El("summary"));
  add(sm, new El("span").set("class", "grow"));
  return { d: d, sm: sm, b: add(sm, new El("button").set("data-chg-file", "undo").set("data-tip", "row" + i).set("class", "icon-btn rd-chg-fx")) };
});
var r = { id: "r1", file_changes: { files: [{ path: "a.txt" }, { path: "<b>.txt" }] } };
applyUndoCheck(card, r, { whole: "conflict", whole_files: ["<b>.txt"], files: { "a.txt": { ok: true }, "<b>.txt": { ok: false, detail: "error: <b>.txt: patch does not apply" } } });
if (ub.getAttribute("aria-disabled") !== "true" || ub.getAttribute("data-tip").indexOf("rd.undo_ck_whole_f") !== 0 || ub.getAttribute("data-tip").indexOf("<b>.txt") < 0) fail("main off " + JSON.stringify(ub.attrs));
if (rows[0].b.getAttribute("aria-disabled") || $(".rd-chg-stale", rows[0].sm)) fail("clean row touched");
var mk = $(".rd-chg-stale", rows[1].sm);
if (rows[1].b.getAttribute("aria-disabled") !== "true" || rows[1].b.getAttribute("data-tip") !== "rd.undo_ck_f" || !mk || mk.textContent !== "rd.chg_stale" || mk.getAttribute("data-tip").indexOf("patch does not apply") < 0) fail("stale row " + JSON.stringify(rows[1].b.attrs));
if (rows[1].sm.children.indexOf(mk) > rows[1].sm.children.indexOf($(".grow", rows[1].sm))) fail("mark after the spacer");
applyUndoCheck(card, r, { whole: "conflict", whole_files: ["<b>.txt"], files: { "<b>.txt": { ok: false } } });
if ($$(".rd-chg-stale", card).length !== 1) fail("mark doubled");
applyUndoCheck(card, r, { whole: "ok", files: { "a.txt": { ok: true }, "<b>.txt": { ok: true } } });
if (ub.getAttribute("aria-disabled") || ub.getAttribute("data-tip") !== "orig" || rows[1].b.getAttribute("data-tip") !== "row1" || $(".rd-chg-stale", card) || rows[1].b.classList.contains("ck-off")) fail("not back on");
applyUndoCheck(card, r, { code: "busy", files: {} });
if (ub.getAttribute("data-tip") !== "rd.undo_err_busy" || rows[0].b.getAttribute("aria-disabled") !== "true" || $(".rd-chg-stale", card)) fail("busy");
applyUndoCheck(card, r, { code: "none", files: {} }); // codes the page already handles change nothing
if (ub.getAttribute("aria-disabled")) fail("none code turned the button off");
ub.disabled = true; ub.removeAttribute("aria-disabled");
applyUndoCheck(card, r, { whole: "conflict", files: {} });
if (ub.getAttribute("aria-disabled")) fail("already disabled button touched");
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
