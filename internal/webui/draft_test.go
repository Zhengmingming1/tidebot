package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestPromptDrafts: a fresh quick run / new task keeps its prompt in sessionStorage, restores
// it with a "Draft restored · Clear" tag, and drops it on submit, sign-out and UI reset. Edit
// & rerun, edit task and "save as task" never touch drafts.
func TestPromptDrafts(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		`else wireDraft(root, ta, "qr", function () { filterChips(); sync(); });`,
		`if (!edit) draftClear("qr");`,
		`var drafty = !taskId && !pre.prompt;`,
		`if (drafty) wireDraft(root, f.prompt, "task");`,
		`if (drafty) draftClear("task");`,
		`state.csrf = ""; draftsClearAll(); showLogin();`,
		"draftFlush();\n      root.innerHTML = \"\";",
		`if (!soft) { draftFlush(); $("#modal-root").innerHTML = "";`,
		`window.addEventListener("pagehide", draftFlush);`,
		`aria-labelledby="tf-plbl"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if strings.Contains(src, "localStorage.setItem(draftKey") {
		t.Error("drafts must stay in sessionStorage (this window only)")
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
		for _, k := range []string{"dr.restored", "dr.clear", "dr.tip"} {
			if m[k] == "" {
				t.Errorf("%s.json: missing %s", lang, k)
			}
		}
		if !strings.Contains(m["dr.tip"], "{t}") {
			t.Errorf("%s.json: dr.tip lost {t}", lang)
		}
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".draft-tag {") {
		t.Error("styles.css lost .draft-tag")
	}
	head := regexp.MustCompile(`(?s)  var DRAFT_MAX = .*?\n  function draftsClearAll\(\) [^\n]*\n`).FindString(src)
	if head == "" {
		t.Fatal("draft helpers not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `
var store = {};
var sessionStorage = { getItem: function (k) { return k in store ? store[k] : null; }, setItem: function (k, v) { store[k] = String(v); }, removeItem: function (k) { delete store[k]; } };
Object.defineProperty(sessionStorage, "keys", { enumerable: false, value: 0 });
var bad = [];
` + head + `
var keysOf = function () { return Object.keys(store); };
draftSave("qr", "line 1\r\nline 2", 1000);
var d = draftLoad("qr", 2000);
if (!d || d.text !== "line 1\r\nline 2" || d.at !== 1000) bad.push("draft not restored as typed");
if (draftLoad("qr", 1000 + DRAFT_TTL + 1)) bad.push("expired draft restored");
draftSave("qr", "   \n", 3000);
if (draftLoad("qr", 3000)) bad.push("blank text kept as draft");
draftSave("qr", new Array(DRAFT_MAX + 2).join("x"), 3000);
if (draftLoad("qr", 3000)) bad.push("over-long draft kept");
store["tb_draft_task"] = "{not json"; if (draftLoad("task")) bad.push("corrupt draft restored");
store["tb_draft_task"] = JSON.stringify({ text: 5, at: Date.now() }); if (draftLoad("task")) bad.push("non-string draft restored");
draftSave("task", "keep me"); draftSave("qr", "me too"); store["tb_lang"] = "en";
var saved = 0; draftPend = { kind: "qr", fn: function () { saved++; }, t: 0 };
draftFlush(); draftFlush();
if (saved !== 1) bad.push("flush ran " + saved + " times");
draftPend = { kind: "task", fn: function () { saved++; }, t: 0 };
draftClear("task");
if (draftPend !== null || draftLoad("task")) bad.push("clear left a pending save or the draft");
draftFlush(); if (saved !== 1) bad.push("cleared draft was saved again");
Object.keys = (function (orig) { return function (o) { return o === sessionStorage ? keysOf() : orig(o); }; })(Object.keys);
draftsClearAll();
if (draftLoad("qr") || store["tb_lang"] !== "en") bad.push("clear-all wrong: " + JSON.stringify(store));
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`
	// The helpers register page listeners at load; stub the browser globals they touch.
	script = `var window = { addEventListener: function () {} }, document = { addEventListener: function () {} };
function esc(s) { return s; } function T(k) { return k; } function $() { return null; } function when() { return ""; }
` + script
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("drafts: %v\n%s", err, out)
	}
}
