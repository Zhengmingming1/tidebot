package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestPaletteLook: "Change theme…" in the command palette opens a page of three themes and
// three colour modes; moving previews (repaint only), Enter saves, Esc / Backspace / closing
// put the old look back, and typing a name on the first page lists that look directly.
func TestPaletteLook(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`{ kind: "cmd", icon: "palette", label: T("pal.look"), kw: T("pal.look_kw"), stay: true, run: function () { palPage("look"); } },`,
		`var close = function () { palRevertLook(); root.innerHTML = "";`,
		`if (pal.page) palPage(""); else close(); }`,
		`else if (e.key === "Backspace" && pal.page && !input.value && e.target === input) { e.preventDefault(); palPage(""); }`,
		"    palDraw();\n    palPreview();",
		`if (it.look) { it.run(); if (pal.close) pal.close(); return; }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, th := range []string{"graphite", "tide", "midnight"} {
		for _, sel := range []string{`.pv-dot[data-pv="` + th + `"] {`, `html[data-mode="dark"] .pv-dot[data-pv="` + th + `"] {`, `html[data-mode="system"] .pv-dot[data-pv="` + th + `"] {`} {
			if !strings.Contains(string(css), sel) {
				t.Errorf("styles.css: no %s", sel)
			}
		}
	}
	for _, lang := range []string{"en", "zh-CN"} {
		raw, _ := os.ReadFile("static/i18n/" + lang + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"pal.look", "pal.look_kw", "pal.look_ph", "pal.current", "pal.back", "pal.k_preview", "pal.k_apply", "pal.k_back", "pal.kind.theme", "pal.kind.mode"} {
			if m[k] == "" {
				t.Errorf("%s.json: no %s", lang, k)
			}
		}
		if !strings.Contains(m["pal.look_set"], "{t}") || !strings.Contains(m["pal.look_set"], "{m}") {
			t.Errorf("%s.json: pal.look_set = %q", lang, m["pal.look_set"])
		}
	}
	look := regexp.MustCompile(`(?s)  function palLookItems.*?\n  function openPalette`).FindString(src)
	if look == "" {
		t.Fatal("palette look helpers not found")
	}
	look = strings.TrimSuffix(look, "  function openPalette")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `var THEMES = ["graphite", "tide", "midnight"], MODES = ["system", "light", "dark"];
function T(k, o) { return o ? k + ":" + o.t + "/" + o.m : k; }
function esc(v) { return String(v); }
var html = { theme: "tide", mode: "dark" }, paints = [], saved = [], toasts = [], marks = [], updates = [];
function curLook() { return { theme: html.theme, mode: html.mode }; }
function applyLook(l) { html.theme = l.theme; html.mode = l.mode; paints.push(l.theme + "/" + l.mode); }
function setLook(l) { applyLook(l); saved.push(l.theme + "/" + l.mode); return { then: function (f) { f(); return { catch: function () {} }; } }; }
function toast(m) { toasts.push(m); }
function fail() {}
function lookMark(l) { marks.push(l.theme + "/" + l.mode); }
function palMatch(label, q) { return !q || label.toLowerCase().indexOf(q.toLowerCase()) >= 0; }
var timers = [];
setTimeout = function (f) { timers.push(f); return timers.length; };
clearTimeout = function (id) { if (id) timers[id - 1] = null; };
function flush() { var t = timers; timers = []; t.forEach(function (f) { if (f) f(); }); }
var el = function () { return { value: "", textContent: "", innerHTML: "", attrs: {}, cls: {}, setAttribute: function (k, v) { this.attrs[k] = v; }, focus: function () {}, classList: { toggle: function () {} } }; };
var dom = { "#pal-q": el(), "#pal-crumb": el(), ".pal-foot": el() };
function $(s) { return dom[s] || null; }
function palUpdate(q) { updates.push(q); pal.items = pal.page === "look" ? palLookItems() : []; pal.sel = 0; }
var pal = { seq: 0, items: [], sel: 0, page: "", look0: null, pv: 0, kept: false };
` + look + `
var bad = [];
function eq(got, want, what) { got = JSON.stringify(got); want = JSON.stringify(want); if (got !== want) bad.push(what + ": got " + got + ", want " + want); }
// first page: names only, current marked, applies on top of what is on screen
var r = palLookItems(true);
eq(r.map(function (i) { return i.label; }), ["set.appearance › theme.graphite", "set.appearance › theme.tide", "set.appearance › theme.midnight", "set.appearance › mode.system", "set.appearance › mode.light", "set.appearance › mode.dark"], "root labels");
eq(r.map(function (i) { return i.sub; }), ["", "pal.current", "", "", "", "pal.current"], "root current");
eq(r[2].look, { theme: "midnight", mode: "dark" }, "midnight keeps the mode");
eq(r[4].look, { theme: "tide", mode: "light" }, "light keeps the theme");
// open the page, move to midnight: repaint only after the beat, nothing saved
palPage("look");
eq(pal.page, "look", "page"); eq(dom["#pal-crumb"].textContent, "set.appearance", "crumb");
eq(dom["#pal-q"].attrs.placeholder, "pal.look_ph", "placeholder");
if (dom[".pal-foot"].innerHTML.indexOf("pal.k_preview") < 0) bad.push("foot not switched");
pal.sel = 2; palPreview(); eq(paints, [], "preview waits"); flush();
eq(html, { theme: "midnight", mode: "dark" }, "preview"); eq(saved, [], "preview saves nothing");
// moving fast: only the last one paints
pal.sel = 0; palPreview(); pal.sel = 4; palPreview(); paints = []; flush();
eq(paints, ["tide/light"], "debounced preview"); eq(pal.items[4].look, { theme: "tide", mode: "light" }, "mode on the opening theme");
// leaving the page puts the old look back
palPage("");
eq(html, { theme: "tide", mode: "dark" }, "back reverts"); eq(pal.page, "", "back"); eq(dom["#pal-q"].attrs.placeholder, "pal.placeholder", "placeholder back");
// a preview pending when leaving does not land afterwards
palPage("look"); pal.sel = 2; palPreview(); palRevertLook(); paints = []; flush(); eq(paints, [], "pending preview dropped");
// Enter keeps it: saved, marked on Settings, toast, and closing no longer reverts
palPage("look"); pal.sel = 2; palPreview(); flush();
pal.items[2].run(); palRevertLook();
eq(saved, ["midnight/dark"], "saved"); eq(marks, ["midnight/dark"], "settings marked"); eq(toasts, ["pal.look_set:theme.midnight/mode.dark"], "toast");
eq(html, { theme: "midnight", mode: "dark" }, "kept after close");
// no item (empty filter) previews the opening look
palPage("look"); pal.items = []; palPreview(); flush(); eq(html, { theme: "midnight", mode: "dark" }, "empty list");
if (bad.length) { console.log(bad.join("\n")); process.exit(1); }
`
	f := t.TempDir() + "/pal.js"
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, f).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
