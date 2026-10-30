package webui

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Settings fill wide windows: the 720px cap is gone, the column is a size
// container, and pages with two or more ordinary cards flow into two columns
// only when the column itself is wide (1024×655 keeps one stacked column).
func TestSettingsFillWideWindows(t *testing.T) {
	cssB, _ := Static.ReadFile("static/styles.css")
	jsB, _ := Static.ReadFile("static/app.js")
	css, js := strings.ReplaceAll(string(cssB), "\r\n", "\n"), strings.ReplaceAll(string(jsB), "\r\n", "\n")

	// The last max-width given to .set-main decides; it must not be the old 720px.
	ms := regexp.MustCompile(`(?m)^\.set-main \{[^}]*max-width: (\d+)px`).FindAllStringSubmatch(css, -1)
	if len(ms) == 0 || ms[len(ms)-1][1] == "720" {
		t.Fatalf(".set-main still capped at 720px: %v", ms)
	}
	for _, want := range []string{
		".set-col { min-width: 0; container: setcol / inline-size; }",
		"@container setcol (min-width: 1040px)",
		".set-main.set-flow { display: block; columns: 2;",
		"break-inside: avoid",
		"column-span: all",
		"@container setcol (min-width: 880px)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("styles.css missing %q", want)
		}
	}
	// At 1024 wide the settings column is ~576px: below both thresholds.
	for _, m := range regexp.MustCompile(`@container setcol \(min-width: (\d+)px\)`).FindAllStringSubmatch(css, -1) {
		if n, _ := strconv.Atoi(m[1]); n < 600 {
			t.Errorf("container threshold %spx would change the 1024×655 layout", m[1])
		}
	}
	if !strings.Contains(js, `'<div class="set-col"><div class="set-main">' + body + "</div></div></div>"`) || !strings.Contains(js, `setFlow($(".set-main"));`) {
		t.Fatal("settings page not wrapped in .set-col or setFlow not called")
	}

	fn := regexp.MustCompile(`(?s)  function setFlow\(main\) \{.*?\n  \}\n`).FindString(js)
	if fn == "" {
		t.Fatal("setFlow not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := fn + `
function el(cls) { var s = new Set(cls ? cls.split(" ") : []); return { classList: { contains: function (c) { return s.has(c); }, toggle: function (c, on) { if (on) s.add(c); else s.delete(c); } }, has: function (c) { return s.has(c); } }; }
function page(list) { var m = el(""); m.children = list.map(el); setFlow(m); return m.has("set-flow"); }
var bad = [];
if (page(["card"])) bad.push("one card flowed");
if (!page(["card", "card"])) bad.push("two cards not flowed");
if (page(["card about-hero", "card"])) bad.push("hero + one card flowed");
if (!page(["card about-hero", "card", "card"])) bad.push("about not flowed");
if (page(["card", "card danger-zone"])) bad.push("card + danger flowed");
if (!page(["card", "card", "card", "card danger-zone"])) bad.push("general not flowed");
setFlow(null);
if (bad.length) { console.error(bad.join("; ")); process.exitCode = 1; }
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("setFlow: %v\n%s", err, out)
	}
}
