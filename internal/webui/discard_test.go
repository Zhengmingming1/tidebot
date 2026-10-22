package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestModalDiscardGuard: Esc, the backdrop, ×/Cancel and Back ask before a dialog with edits
// closes; the close() given to onMount (after a save) doesn't. Only fields the user touched and
// changed count; prefills, search boxes, [data-noguard] and CRLF-only differences don't.
func TestModalDiscardGuard(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		`if (e.key === "Escape") { e.preventDefault(); tryClose(); return; }`,
		`if (e.target.classList.contains("modal-bg")) tryClose();`,
		`$$("[data-close]", root).forEach(function (b) { b.addEventListener("click", tryClose); });`,
		`if (onMount) onMount(root, close);`,
		`if (e.isTrusted && guardable(e.target)) e.target._tbTouched = true;`,
		`history.replaceState(null, "", lastHash || "#/");`,
		`if (!state.native && modalGuard && modalGuard.dirty())`,
		`spellcheck="false" data-noguard>`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
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
		for _, k := range []string{"cf.discard_q", "cf.keep_editing", "cf.discard"} {
			if m[k] == "" {
				t.Errorf("%s.json: missing %s", lang, k)
			}
		}
	}
	var fns []string
	for _, name := range []string{"fieldVal", "guardable", "guardBase", "guardDirty"} {
		fn := regexp.MustCompile(`(?s)  function ` + name + `\(.*?\n`).FindString(src)
		if strings.Count(fn, "{") != strings.Count(fn, "}") { // multi-line body
			fn = regexp.MustCompile(`(?s)  function ` + name + `\(.*?\n  \}\n`).FindString(src)
		}
		if fn == "" {
			t.Fatalf("%s not found", name)
		}
		fns = append(fns, fn)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := strings.Join(fns, "") + `
function F(tag, type, value, noguard) { return { tagName: tag, type: type, value: value, checked: false, closest: function () { return noguard ? {} : null; } }; }
var els = [];
function $$() { return els; }
var bad = [];
var ta = F("TEXTAREA", "textarea", "line 1\nline 2"), name = F("INPUT", "text", "auto name"),
    q = F("INPUT", "search", ""), typed = F("INPUT", "text", "", true), cb = F("INPUT", "checkbox", "on");
els = [ta, name, q, typed, cb];
name.value = "prefilled later";            // async prefill before the user touched anything
guardBase({});
if (guardDirty({})) bad.push("fresh dialog is dirty");
name.value = "auto-filled from prompt";     // auto-fill while typing: not touched by the user
if (guardDirty({})) bad.push("auto-fill counted as an edit");
ta._tbTouched = true; ta.value = "line 1\r\nline 2";
if (guardDirty({})) bad.push("CRLF-only change counted");
ta.value = "line 1\nline 2 more";
if (!guardDirty({})) bad.push("typed prompt not detected");
ta.value = "line 1\nline 2";
if (guardDirty({})) bad.push("undone edit still dirty");
q._tbTouched = true; q.value = "filter";
typed._tbTouched = true; typed.value = "name to confirm";
if (guardDirty({})) bad.push("search / data-noguard counted");
cb._tbTouched = true; cb.checked = true;
if (!guardDirty({})) bad.push("checkbox change not detected");
cb.checked = false;
var late = F("SELECT", "select-one", "a"); els.push(late); guardBase({}); late._tbTouched = true; late.value = "b";
if (!guardDirty({})) bad.push("field added later not tracked");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("guard: %v\n%s", err, out)
	}
}
