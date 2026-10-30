package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestDialogSubmitKey: ⌘/Ctrl+Enter anywhere in a dialog or drawer presses its main button —
// the focused form's primary submit, else the footer's last primary, a red confirm only when
// there is no primary — and never while the discard bar asks, an input method composes, a
// field already took the key, the button is off or hidden; auto-repeat does not press twice.
func TestDialogSubmitKey(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		"    dialogKeys(box);\n    if (onMount) onMount(root, close);",
		"    dialogKeys(d);\n    fill(d);",
		`if (!submitKey(e) || e.defaultPrevented || e.isComposing || e.keyCode === 229) return false;`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".modal .actions > .btn:last-child[data-tip]:hover::after") {
		t.Error("footer tooltip not kept inside the dialog")
	}
	for _, lang := range []string{"en", "zh-CN"} {
		raw, _ := os.ReadFile("static/i18n/" + lang + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(m["dlg.key"], "{k}") {
			t.Errorf("%s.json: dlg.key has no {k}: %q", lang, m["dlg.key"])
		}
		if !strings.Contains(m["sc.submit"], map[string]string{"en": "dialog", "zh-CN": "弹窗"}[lang]) {
			t.Errorf("%s.json: sc.submit does not mention dialogs: %q", lang, m["sc.submit"])
		}
	}
	keys := regexp.MustCompile(`(?s)  var KB_MAC = .*?\n  // ---------- i18n`).FindString(src)
	dlg := regexp.MustCompile(`(?s)  var DLG_FOOT = .*?\n  // Modal: role=dialog`).FindString(src)
	if keys == "" || dlg == "" {
		t.Fatal("helpers not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(v) { return String(v); }
function T(k, o) { return k === "dlg.key" ? o.k + " from any field" : k; }
` + keys + "\n" + dlg + `
var bad = [];
function eq(got, want, what) { if (got !== want) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
// A tiny stand-in DOM: buttons know their form, footer and hidden ancestor.
function btn(o) {
  o = o || {};
  var b = { type: o.type || "button", disabled: !!o.disabled, offsetParent: o.gone ? null : {}, form: o.form || null, clicks: 0, attrs: {},
    cls: o.cls || [], foot: o.foot !== false, hid: !!o.hid };
  b.classList = { contains: function (c) { return b.cls.indexOf(c) >= 0; } };
  b.closest = function (s) { if (s === "form") return b.form; if (s === DLG_FOOT) return b.foot ? {} : null; if (s === "button") return b; return b.hid ? {} : null; };
  b.click = function () { b.clicks++; };
  b.getAttribute = function (k) { return k in b.attrs ? b.attrs[k] : null; };
  b.setAttribute = function (k, v) { b.attrs[k] = String(v); };
  return b;
}
function form(btns) { var f = { subs: 0, by: null, btns: btns }; f.requestSubmit = function (b) { f.subs++; f.by = b; }; btns.forEach(function (b) { b.form = f; }); return f; }
function box(btns, discard) { return { btns: btns, discard: !!discard, contains: function () { return true; } }; }
$ = function (s, r) { return s === ".discard-bar" && r.discard ? {} : null; };
$$ = function (s, r) { return (s === "button" ? r.btns : r.btns).slice(); };
function key(o) { var e = Object.assign({ key: "Enter", ctrlKey: true, prevented: false, stopped: false }, o); e.preventDefault = function () { e.prevented = true; e.defaultPrevented = true; }; e.stopPropagation = function () { e.stopped = true; }; return e; }
function field(f) { return { closest: function (s) { return s === "form" ? f : null; } }; }

// Form: Save (submit) + Save & test (primary submit) → the primary submit.
var save = btn({ type: "submit", cls: ["btn"] }), test = btn({ type: "submit", cls: ["btn", "primary"] }), cancel = btn({ cls: ["btn", "ghost"] });
var f = form([cancel, save, test]), bx = box([cancel, save, test]);
var e = key({ target: field(f) });
eq(dialogKeySubmit(e, bx), true, "handled");
eq(f.subs, 1, "form submitted once"); eq(f.by, test, "by the primary submit"); eq(e.prevented && e.stopped, true, "key consumed");
// Mac ⌘Enter too; auto-repeat consumed but not pressed again.
eq(dialogKeySubmit(key({ ctrlKey: false, metaKey: true, target: field(f) }), bx), true, "cmd");
eq(f.subs, 2, "cmd submits");
eq(dialogKeySubmit(key({ repeat: true, target: field(f) }), bx), true, "repeat consumed"); eq(f.subs, 2, "repeat not submitted");
// Not ours: plain Enter, AltGr, composing, a field that took it, discard bar asking.
eq(dialogKeySubmit(key({ ctrlKey: false, target: field(f) }), bx), false, "plain Enter");
eq(dialogKeySubmit(key({ altKey: true, target: field(f) }), bx), false, "AltGr");
eq(dialogKeySubmit(key({ isComposing: true, target: field(f) }), bx), false, "IME");
eq(dialogKeySubmit(key({ keyCode: 229, target: field(f) }), bx), false, "IME 229");
eq(dialogKeySubmit(key({ defaultPrevented: true, target: field(f) }), bx), false, "field handled it");
eq(dialogKeySubmit(key({ target: field(f) }), box([cancel, save, test], true)), false, "discard bar");
eq(f.subs, 2, "nothing submitted by the refusals");
// Disabled primary submit → the other submit; none usable → nothing.
test.disabled = true; dialogKeySubmit(key({ target: field(f) }), bx); eq(f.by, save, "falls back to the other submit");
save.disabled = true; eq(dialogKeySubmit(key({ target: field(f) }), bx), false, "all submits off");
// No form: footer primary is clicked; hidden / gone / outside the footer ones are not.
var other = btn({ cls: ["btn", "primary"], foot: false }), hidden = btn({ cls: ["btn", "primary"], hid: true }), gone = btn({ cls: ["btn", "primary"], gone: true });
var done = btn({ cls: ["btn", "primary"] }), copy = btn({ cls: ["btn"] });
var b2 = box([copy, done, other, hidden, gone]);
eq(dialogKeySubmit(key({ target: { closest: function () { return null; } } }), b2), true, "footer primary");
eq(done.clicks, 1, "done clicked"); eq(other.clicks + hidden.clicks + gone.clicks + copy.clicks, 0, "others untouched");
// A red confirm counts only without a primary.
var del = btn({ cls: ["btn", "danger-solid"] }), b3 = box([btn({ cls: ["btn", "ghost"] }), del]);
dialogKeySubmit(key({ target: null }), b3); eq(del.clicks, 1, "danger confirm");
var b4 = box([del, done]); dialogKeySubmit(key({ target: null }), b4); eq(done.clicks, 2, "primary wins over danger"); eq(del.clicks, 1, "danger not pressed");
// Nothing to press → key left alone.
var e5 = key({ target: null }); eq(dialogKeySubmit(e5, box([copy])), false, "no main button"); eq(!!e5.prevented, false, "not consumed");
// Tooltip hint: added once, appended to an existing tip, not doubled when it names the key.
KB_MAC = false;
var h1 = btn(); dialogKeyHint(h1); dialogKeyHint(h1); eq(h1.attrs["data-tip"], "Ctrl+Enter from any field", "hint alone"); eq(h1.attrs["aria-keyshortcuts"], "Control+Enter", "aria pc");
var h2 = btn(); h2.attrs["data-tip"] = "Runs now"; dialogKeyHint(h2); eq(h2.attrs["data-tip"], "Runs now · Ctrl+Enter", "hint appended");
var h3 = btn(); h3.attrs["data-tip"] = "Run (Ctrl+Enter)"; dialogKeyHint(h3); eq(h3.attrs["data-tip"], "Run (Ctrl+Enter)", "hint kept");
KB_MAC = true;
var h4 = btn(); dialogKeyHint(h4); eq(h4.attrs["data-tip"], "⌘Enter from any field", "hint mac"); eq(h4.attrs["aria-keyshortcuts"], "Meta+Enter", "aria mac");
console.log(JSON.stringify(bad));
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
