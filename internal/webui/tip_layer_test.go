package webui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTipLayerPlacement: [data-tip] text is shown in one fixed layer, so a container with
// overflow hidden or the window edge no longer cuts it off (the old ::after-only tips were
// clipped 13 times per language on the main pages at 1024×655). tipPlace opens above,
// flips below when there is no room (and back), and keeps the tip 8px inside the window.
func TestTipLayerPlacement(t *testing.T) {
	js := appJS(t)
	start := strings.Index(js, "\n  var TIP_GAP = 6, TIP_PAD = 8;")
	end := strings.Index(js, "\n  var tipSt = {")
	if start < 0 || end < start {
		t.Fatal("tipPlace section not found")
	}
	runNode(t, `
eval(SECTION);
var bad = [];
function r(l, t, w, h) { return { left: l, top: t, width: w, height: h, right: l + w, bottom: t + h }; }
function eq(name, got, x, y, below) { if (got.x !== x || got.y !== y || got.below !== below) bad.push(name + ": " + JSON.stringify(got)); }
// room above: centred above the element
eq("above", tipPlace(r(400, 300, 100, 20), 120, 30, 1024, 655, false), 390, 264, false);
// asked for below and there is room: below
eq("below", tipPlace(r(400, 300, 100, 20), 120, 30, 1024, 655, true), 390, 326, true);
// near the top (home KPI strip, top bar): flips below
eq("flip down", tipPlace(r(400, 20, 100, 20), 120, 30, 1024, 655, false), 390, 46, true);
// asked for below at the bottom of the window: flips above
eq("flip up", tipPlace(r(400, 610, 100, 20), 120, 30, 1024, 655, true), 390, 574, false);
// right edge (send button, grip, download): shifted left to stay 8px inside
eq("right edge", tipPlace(r(980, 300, 30, 20), 280, 35, 1024, 655, false), 736, 259, false);
// left edge: shifted right
eq("left edge", tipPlace(r(0, 300, 30, 20), 280, 35, 1024, 655, false), 8, 259, false);
// no room either way: stays on the asked side, clamped into the window
eq("tall", tipPlace(r(400, 100, 100, 20), 200, 600, 1024, 655, false), 350, 8, false);
// wider than the window: pinned to the left padding
eq("wide", tipPlace(r(100, 300, 50, 20), 2000, 30, 1024, 655, false), 8, 264, false);
if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
`, map[string]string{"SECTION": js[start:end]})

	body := js[end:]
	body = body[:strings.Index(body, "\n  document.addEventListener(\"keydown\", function (e) { if (e.key === \"Escape\") tipHide(); });")]
	for _, want := range []string{
		`L.textContent = text;`,              // text, never HTML
		`L.setAttribute("role", "tooltip");`, // announced as a tooltip
		`t.setAttribute("aria-describedby", "tip-layer")`,
		`attributeFilter: ["data-tip", "aria-expanded"]`, // live text; closes when its menu opens
		`if (e.pointerType === "touch") return;`,
		`t.matches(":focus-visible")`, // keyboard focus shows it too
		`document.documentElement.classList.add("tip-js");`,
		`window.addEventListener("hashchange", tipHide);`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tip layer: missing %s", want)
		}
	}
	if strings.Contains(body, "innerHTML") {
		t.Error("tip layer must not build HTML from data-tip")
	}
	css, err := os.ReadFile(filepath.Join("src", "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	cs := strings.ReplaceAll(string(css), "\r\n", "\n")
	if !strings.Contains(cs, "html.tip-js [data-tip]:hover::after, html.tip-js [data-tip]:focus-visible::after { display: none !important; }") {
		t.Error("the ::after fallback must step aside once the layer is on")
	}
	i := strings.Index(cs, "\n.tip-layer {")
	if i < 0 {
		t.Fatal(".tip-layer rule missing")
	}
	rule := cs[i : i+strings.Index(cs[i:], "}")]
	for _, want := range []string{"position: fixed", "pointer-events: none", "var(--tip-bg)", "var(--tip-fg)", "z-index: 10050"} {
		if !strings.Contains(rule, want) {
			t.Errorf(".tip-layer: missing %s", want)
		}
	}
}

// TestTipLayerNamesDoNotShadow: every source file shares one closure, so a top-level name in
// tooltips.js that another file also declares silently replaces it (a "var tip" here once
// broke the task form with "tip is not a function").
func TestTipLayerNamesDoNotShadow(t *testing.T) {
	decl := regexp.MustCompile(`(?m)^  (?:function\s+([A-Za-z_$][\w$]*)|var\s+([A-Za-z_$][\w$]*))`)
	names := func(src string) map[string]bool {
		m := map[string]bool{}
		for _, g := range decl.FindAllStringSubmatch(src, -1) {
			m[g[1]+g[2]] = true
			// var a = 1, b = 2 on one line
			if g[2] != "" {
				line := src[strings.Index(src, g[0]):]
				line = line[:strings.IndexByte(line, '\n')]
				for _, more := range regexp.MustCompile(`,\s*([A-Za-z_$][\w$]*)\s*=`).FindAllStringSubmatch(line, -1) {
					m[more[1]] = true
				}
			}
		}
		return m
	}
	own, err := os.ReadFile(filepath.Join("src", "tooltips.js"))
	if err != nil {
		t.Fatal(err)
	}
	mine := names(string(own))
	if !mine["tipShow"] || !mine["TIP_PAD"] {
		t.Fatalf("declaration scan missed tooltips.js names: %v", mine)
	}
	files, _ := filepath.Glob(filepath.Join("src", "*.js"))
	for _, f := range files {
		if filepath.Base(f) == "tooltips.js" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for n := range names(string(b)) {
			if mine[n] {
				t.Errorf("tooltips.js declares %s, which %s also declares", n, filepath.Base(f))
			}
		}
	}
}
