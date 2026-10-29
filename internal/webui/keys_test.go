package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestPlatformKeys: shortcut labels follow the OS (⌘K / ⌘Enter on a Mac, Ctrl+K / Ctrl+Enter
// elsewhere), the palette opens only with the platform's modifier (Ctrl+K stays text editing
// on a Mac, Ctrl+Alt is AltGr on Windows), and letter shortcuts work with Caps Lock and
// non-Latin layouts.
func TestPlatformKeys(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`"</span>" + kbdHtml("Mod+K") + "</button>"`,
		`var rows = [["Mod+K", T("sc.palette")]`,
		`["Mod+Enter", T("sc.submit")]`,
		`r[0].split(" / ").map(kbdHtml).join(" / ")`,
		`T("qr.run_key", { k: keyLabel("Mod+Enter") })`,
		`T("ie.keys_multi", { k: keyLabel("Mod+Enter") })`,
		`T("pv.sort_hint", { k: keyLabel("Alt+↑/↓") })`,
		`if (modKey(e) && key.toLowerCase() === "k") { e.preventDefault(); openPalette(); return; }`,
		`if (key === "g" || key === "n")`,
		"function kbKey(e) {\n    var k = kbLetter(e);",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if n := strings.Count(src, `T("bt.sort_hint", { k: keyLabel("Alt+← →") })`); n != 2 {
		t.Errorf("bt.sort_hint with key label: %d uses, want 2", n)
	}
	// One platform check only, no hand-written modifier names in markup.
	if n := strings.Count(src, "navigator.platform"); n != 0 {
		t.Errorf("navigator.platform read in %d places outside KB_MAC", n)
	}
	if regexp.MustCompile(`"(⌘|Ctrl) ?K`).MatchString(src) {
		t.Error("hard-coded ⌘ K / Ctrl K label")
	}
	if n := len(regexp.MustCompile(`\(e\.ctrlKey \|\| e\.metaKey\)|\(e\.metaKey \|\| e\.ctrlKey\)`).FindAllString(src, -1)); n != 1 { // submitKey's own
		t.Error("modifier check outside modKey / submitKey")
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
		for _, k := range []string{"bt.sort_hint", "ie.keys_multi", "pv.sort_hint", "qr.run_key"} {
			if !strings.Contains(m[k], "{k}") {
				t.Errorf("%s.json: %s has no {k}: %q", lang, k, m[k])
			}
		}
		if m["sc.submit"] == "" {
			t.Errorf("%s.json: missing sc.submit", lang)
		}
		for k, v := range m {
			if regexp.MustCompile(`\b(Ctrl|Alt)\s*\+|⌘`).MatchString(v) {
				t.Errorf("%s.json: %s names a modifier itself: %q", lang, k, v)
			}
		}
	}
	block := regexp.MustCompile(`(?s)  var KB_MAC = .*?\n  // ---------- i18n`).FindString(src)
	escFn := regexp.MustCompile(`(?s)  function esc\(v\) \{.*?\n  \}\n`).FindString(src)
	if block == "" || escFn == "" {
		t.Fatal("key helpers not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := escFn + block + `
var bad = [];
function eq(got, want, what) { if (got !== want) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
eq(keyLabel("Mod+K", false, true), "⌘K", "mac Mod+K");
eq(keyLabel("Mod+K", false, false), "Ctrl+K", "pc Mod+K");
eq(keyLabel("Mod+Enter", false, true), "⌘Enter", "mac Mod+Enter");
eq(keyLabel("Mod+Enter", false, false), "Ctrl+Enter", "pc Mod+Enter");
eq(keyLabel("Alt+↑/↓", false, true), "⌥↑/↓", "mac Alt");
eq(keyLabel("Alt+← →", false, false), "Alt+← →", "pc Alt");
eq(keyLabel("Mod+Shift+P", false, true), "⌘⇧P", "mac chord");
eq(keyLabel("Mod+Shift+P", false, false), "Ctrl+Shift+P", "pc chord");
eq(keyLabel("Mod+K", true, true), "Command+K", "mac aria");
eq(keyLabel("Alt+↑", true, true), "Option+↑", "mac aria alt");
eq(keyLabel("Mod+K", true, false), "Control+K", "pc aria");
eq(keyLabel("j"), "j", "plain key");
eq(keyLabel("?"), "?", "plain ?");
eq(kbdHtml("j"), '<kbd aria-label="j">j</kbd>', "kbd plain");
eq(kbdHtml("g h"), '<kbd aria-label="g">g</kbd><kbd aria-label="h">h</kbd>', "kbd sequence");
eq(kbdHtml("Mod+K").indexOf('<kbd aria-label="'), 0, "kbd chord");
// ⌘ on a Mac, Ctrl elsewhere; never Ctrl+Alt (AltGr) or the other modifier.
eq(modKey({ metaKey: true }, true), true, "mac cmd");
eq(modKey({ ctrlKey: true }, true), false, "mac ctrl stays text editing");
eq(modKey({ metaKey: true, ctrlKey: true }, true), false, "mac cmd+ctrl");
eq(modKey({ metaKey: true, shiftKey: true }, true), true, "mac cmd+shift");
eq(modKey({ ctrlKey: true }, false), true, "pc ctrl");
eq(modKey({ metaKey: true }, false), false, "pc win key");
eq(modKey({ ctrlKey: true, altKey: true }, false), false, "pc AltGr");
eq(modKey({}, false), false, "pc none");
eq(submitKey({ key: "Enter", metaKey: true }), true, "submit cmd");
eq(submitKey({ key: "Enter", ctrlKey: true }), true, "submit ctrl");
eq(submitKey({ key: "Enter" }), false, "plain Enter");
eq(submitKey({ key: "Enter", ctrlKey: true, altKey: true }), false, "submit AltGr");
eq(submitKey({ key: "k", ctrlKey: true }), false, "submit other key");
// Letters by meaning: Caps Lock, Shift, other layouts.
eq(kbLetter({ key: "j", code: "KeyJ" }), "j", "j");
eq(kbLetter({ key: "J", code: "KeyJ" }), "j", "Caps Lock J");
eq(kbLetter({ key: "J", code: "KeyJ", shiftKey: true }), "J", "Shift+J stays");
eq(kbLetter({ key: "о", code: "KeyJ" }), "j", "Russian layout");
eq(kbLetter({ key: "κ", code: "KeyK" }), "k", "Greek layout");
eq(kbLetter({ key: "л", code: "KeyK" }).toLowerCase(), "k", "palette k on Russian");
eq(kbLetter({ key: "Enter", code: "Enter" }), "Enter", "Enter");
eq(kbLetter({ key: "/", code: "Slash" }), "/", "slash");
eq(kbLetter({ key: "?", code: "Slash", shiftKey: true }), "?", "question mark");
eq(kbLetter({ key: ",", code: "Comma" }), ",", "comma");
eq(kbLetter({ key: "é", code: "Digit2" }), "é", "French digit row");
eq(kbLetter({ key: "Escape" }), "Escape", "Escape");
eq(typeof KB_MAC, "boolean", "KB_MAC");
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
