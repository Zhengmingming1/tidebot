package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestListKeyboardCursor: j / k pick a row on list pages, Enter / o open it, x ticks it, r runs
// it, Esc drops it, / focuses the search box; the row is remembered per page and survives a
// repaint; a row remembered from a click is only highlighted (o keeps its page meaning).
func TestListKeyboardCursor(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		"    if (kbKey(e)) return;\n    // page-level single keys",
		`'<tr class="click" data-href="#/skills/' + enc(s.id) + '">`,
		`esc(T("tl.search")) + '" value="' + esc(v.q) + '">' + KB_SLASH + '</label>'`,
		`esc(T("runs.search")) + '" value="' + esc(v.q) + '">' + KB_SLASH + '</label>'`,
		`["j / k", T("sc.rows")], ["Enter / o", T("sc.open_row")], ["x / r", T("sc.row_acts")], ["/", T("sc.search")]`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".table tbody tr.kb-cur td", "li.kb-cur", ".search-in input:not(:placeholder-shown) ~ .kb-hint"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lost %q", want)
		}
	}
	for _, lang := range []string{"en", "zh-CN"} {
		raw, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"sc.rows", "sc.open_row", "sc.row_acts", "sc.search", "sc.move"} {
			if m[k] == "" {
				t.Errorf("%s.json: missing %s", lang, k)
			}
		}
	}
	block := regexp.MustCompile(`(?s)  // Enter / o / x / r only act.*?\n  \}\)\(\);\n`).FindString(src)
	if block == "" {
		t.Fatal("list cursor block not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	// A tiny fake DOM: rows with data-href, a checkbox and a run button in each.
	script := `
var bad = [], clicks = [], listeners = {}, focused = null;
function El(tag, attrs, kids) {
  this.tagName = tag; this.attrs = attrs || {}; this.kids = kids || []; this.cls = {}; this.offsetParent = {}; this.disabled = false;
  var me = this; this.kids.forEach(function (k) { k.parent = me; });
  this.classList = { add: function (c) { me.cls[c] = 1; }, remove: function (c) { delete me.cls[c]; }, contains: function (c) { return !!me.cls[c]; } };
}
El.prototype.getAttribute = function (k) { return k in this.attrs ? this.attrs[k] : null; };
El.prototype.setAttribute = function (k, v) { this.attrs[k] = String(v); };
El.prototype.removeAttribute = function (k) { delete this.attrs[k]; };
El.prototype.hasAttribute = function (k) { return k in this.attrs; };
El.prototype.focus = function () { focused = this; };
El.prototype.select = function () {};
El.prototype.scrollIntoView = function () { this.scrolled = (this.scrolled || 0) + 1; };
El.prototype.click = function () { clicks.push(this.attrs.id); };
El.prototype.closest = function (sel) { var e = this; while (e) { if (sel.split(",").some(function (s) { return e.tagName === s.toUpperCase() || (s === "[tabindex]" && e.hasAttribute && e.hasAttribute("tabindex")); })) return e; e = e.parent; } return null; };
El.prototype.all = function () { var out = []; this.kids.forEach(function (k) { out.push(k); out = out.concat(k.all()); }); return out; };
function row(id) { return new El("TR", { "data-href": "#/tasks/" + id }, [new El("INPUT", { type: "checkbox", "data-pick": id, id: "pick-" + id }), new El("BUTTON", { "data-runnow": id, id: "run-" + id })]); }
var search = new El("INPUT", { type: "search", id: "q" });
var pg = new El("MAIN", { id: "page" }, [search, row("a"), row("b"), row("c")]);
var hidden = row("h"); hidden.offsetParent = null; pg.kids.push(hidden); hidden.parent = pg;
function match(el, sel) {
  if (sel === "[data-href]") return el.hasAttribute("data-href");
  if (sel === ".kb-cur") return el.classList.contains("kb-cur");
  if (sel === "input[type=search]") return el.tagName === "INPUT" && el.attrs.type === "search";
  if (sel === "input[type=checkbox][data-pick]") return el.tagName === "INPUT" && el.hasAttribute("data-pick");
  if (sel === "[data-runnow]") return el.hasAttribute("data-runnow");
  return false;
}
function $$(sel, root) { return (root || pg).all().filter(function (e) { return match(e, sel); }); }
function $(sel, root) { return $$(sel, root)[0] || null; }
function page() { return pg; }
var location = { hash: "#/tasks" };
var document = { activeElement: null, body: {}, addEventListener: function (n, f) { listeners[n] = f; } };
Object.defineProperty(document, "activeElement", { get: function () { return focused; } });
var window = { addEventListener: function (n, f) { listeners["w" + n] = f; } };
var MutationObserver = function (f) { this.observe = function () { listeners.mo = f; }; };
var requestAnimationFrame = function (f) { f(); return 0; };
var prefetched = [];
function prefetchRoute(h) { prefetched.push(h); }
` + block + `
function key(k) { var e = { key: k, prevented: false, preventDefault: function () { this.prevented = true; } }; return { used: kbKey(e), prevented: e.prevented }; }
var rows = $$("[data-href]").filter(function (r) { return r.offsetParent; });
if (kbStep(0, -1, 1) !== -1 || kbStep(3, -1, 1) !== 0 || kbStep(3, -1, -1) !== 2 || kbStep(3, 2, 1) !== 2 || kbStep(3, 0, -1) !== 0) bad.push("kbStep clamps wrong");
if (key("o").used || key("x").used || key("Enter").used) bad.push("keys acted with no row picked");
key("j");
if (!rows[0].classList.contains("kb-cur") || rows[0].getAttribute("aria-current") !== "true" || focused !== rows[0] || !rows[0].scrolled) bad.push("first j did not pick, focus and scroll row a");
if (prefetched.join() !== "#/tasks/a") bad.push("j did not prefetch the picked row: " + prefetched);
key("j"); key("j"); key("j");
if (!rows[2].classList.contains("kb-cur") || rows[0].classList.contains("kb-cur")) bad.push("j did not stop at the last visible row");
if (hidden.classList.contains("kb-cur")) bad.push("hidden row picked");
key("k");
if (!rows[1].classList.contains("kb-cur") || rows[2].classList.contains("kb-cur") || rows[2].hasAttribute("tabindex")) bad.push("k did not move back / clean up");
key("x"); key("r");
if (clicks.join(",") !== "pick-b,run-b") bad.push("x / r clicked " + clicks.join(","));
// A repaint replaces the rows: the same link gets the highlight back.
pg.kids = [search, row("a"), row("b"), row("c")]; pg.kids.forEach(function (k) { k.parent = pg; });
focused = null; // a removed element loses focus to <body> in a browser
listeners.mo();
var fresh = $$("[data-href]");
if (!fresh[1].classList.contains("kb-cur")) bad.push("highlight lost on repaint");
var r = key("Enter");
if (!r.used || location.hash !== "#/tasks/b") bad.push("Enter did not open the row: " + location.hash);
// Another page, then back: highlighted, but o / Enter wait for j / k (click memory only).
listeners.whashchange(); location.hash = "#/tasks";
listeners.mo();
if (!fresh[1].classList.contains("kb-cur")) bad.push("row not highlighted on return");
if (key("o").used) bad.push("o acted on a remembered (not picked) row");
// A focused button keeps its own Enter.
key("j"); focused = fresh[0].kids[1];
if (key("Enter").used) bad.push("Enter stolen from a focused button");
focused = null;
key("Escape");
if (fresh.some(function (x) { return x.classList.contains("kb-cur"); })) bad.push("Esc did not drop the highlight");
var s = key("/");
if (!s.used || !s.prevented || focused !== search) bad.push("/ did not focus the search box");
// Mouse click on a row remembers it.
listeners.click({ target: { closest: function () { return fresh[2]; } } });
if (kbCur["#/tasks"] !== "#/tasks/c") bad.push("click not remembered");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("list cursor: %v\n%s", err, out)
	}
}
