package webui

import (
	"strings"
	"testing"
)

// TestProviderHeadUI: Settings › Providers keeps "Add provider" and the write-only note in
// the card head (button right-aligned, note as a hover tip), not in an extra row under it.
// The button is wired by assignment so re-rendering the list doesn't stack handlers, and a
// click that lands before the list has loaded (palette "n p") still opens the picker.
func TestProviderHeadUI(t *testing.T) {
	jsb, _ := Static.ReadFile("static/app.js")
	cssb, _ := Static.ReadFile("static/styles.css")
	js, css := string(jsb), string(cssb)
	i := strings.Index(js, `section === "providers"`)
	if i < 0 {
		t.Fatal("providers section not found")
	}
	sec := js[i : i+900]
	for _, want := range []string{
		`head.replace("</h2></div>", "</h2>" + tip(T("pv.sub"))`,
		`class="btn primary sm card-act" id="pv-add"`,
		`$("#pv-add").onclick = function (e) { e.currentTarget._want = true; }`,
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("providers head: missing %s", want)
		}
	}
	for _, want := range []string{`addBtn.onclick = function ()`, `if (addBtn._want) { addBtn._want = false; addBtn.onclick(); }`} {
		if !strings.Contains(js, want) {
			t.Errorf("providerCards: missing %s", want)
		}
	}
	for _, gone := range []string{"pv-toolbar", `$("#pv-add", box)`} {
		if strings.Contains(js, gone) || strings.Contains(css, gone) {
			t.Errorf("leftover %s", gone)
		}
	}
	if !strings.Contains(css, ".card-head .card-act { margin-left: auto;") {
		t.Error("card-act not right-aligned in the card head")
	}
	if !strings.Contains(css, "#pv-box { padding-top: var(--sp-4); }") {
		t.Error("provider list should start 16px below the head divider")
	}
}

// The preset picker's arrow-key handler must not touch the grid after Esc closed it
// (getComputedStyle(null) threw a TypeError on every Esc).
func TestPresetPickerEscNoError(t *testing.T) {
	jsb, _ := Static.ReadFile("static/app.js")
	js := string(jsb)
	if !strings.Contains(js, `var grid = $(".ap-grid", root); if (!grid) return;`) || strings.Contains(js, `getComputedStyle($(".ap-grid", root))`) {
		t.Fatal("preset picker: grid not guarded after close")
	}
}
