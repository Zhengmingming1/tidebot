package webui

import (
	"regexp"
	"strings"
	"testing"
)

// The WebUI runs in WKWebView / Safari (macOS), WebView2 / Chrome / Edge (Windows), WebKitGTK
// (Linux desktop) and Firefox. These checks keep scrollbars, fonts and native controls the same
// in all of them.
func TestCrossPlatformCSS(t *testing.T) {
	raw, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Fatal("styles.css: unbalanced braces")
	}

	// Scrollbars: styled with ::-webkit-scrollbar for every Chromium / WebKit engine.
	for _, s := range []string{
		"::-webkit-scrollbar { width: 8px; height: 8px; }",
		"::-webkit-scrollbar-thumb { background: var(--sb-thumb) padding-box;",
		"::-webkit-scrollbar-thumb:hover",
		"--sb-thumb: #",
	} {
		if !strings.Contains(css, s) {
			t.Errorf("styles.css lacks %q", s)
		}
	}
	// scrollbar-width / scrollbar-color only for Firefox: Chromium 121+ drops the
	// ::-webkit-scrollbar styles of any element that has them.
	ff := regexp.MustCompile(`(?s)@supports not selector\(::-webkit-scrollbar\) \{\n(.*?)\n\}`).FindStringSubmatch(css)
	if ff == nil || !strings.Contains(ff[1], "scrollbar-width: thin;") || !strings.Contains(ff[1], "scrollbar-color: var(--sb-thumb) transparent;") {
		t.Fatal("Firefox scrollbar fallback missing")
	}
	rest := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(strings.Replace(css, ff[0], "", 1), "")
	for _, s := range []string{"scrollbar-width", "scrollbar-color", "scrollbar-gutter"} {
		if strings.Contains(rest, s) {
			t.Errorf("%s outside the Firefox-only block (Chromium would ignore the styled scrollbar, WKWebView leaves no gutter)", s)
		}
	}

	// The page keeps its scrollbar lane, so content width never changes between pages.
	if !strings.Contains(css, "\nhtml { overflow-y: scroll; }") {
		t.Error("html must keep overflow-y: scroll")
	}
	for _, m := range regexp.MustCompile(`(?m)^(html|body|html, body)\s*\{[^}]*\}`).FindAllString(css, -1) {
		if regexp.MustCompile(`overflow(-y)?:\s*(auto|hidden)`).MatchString(m) {
			t.Errorf("rule takes the page scrollbar lane away: %s", m)
		}
	}

	// Native controls: light under a dark OS theme, accent-coloured checkboxes and radios.
	if !strings.Contains(css, ":root { color-scheme: light; accent-color: var(--accent); }") {
		t.Error("color-scheme / accent-color missing on :root")
	}

	// Dialogs and popup lists don't scroll the page behind them.
	oc := regexp.MustCompile(`(?s)\n(\.modal, [^{]*)\{ overscroll-behavior: contain; \}`).FindStringSubmatch(css)
	if oc == nil {
		t.Fatal("overscroll-behavior: contain rule missing")
	}
	sel := map[string]bool{}
	for _, s := range strings.Split(oc[1], ",") {
		sel[strings.TrimSpace(s)] = true
	}
	for _, s := range []string{".modal", ".drawer-body", "#pal-list", ".notif-center", ".search-results", ".tx-list", ".fb-code"} {
		if !sel[s] {
			t.Errorf("overscroll-behavior: contain lacks %s", s)
		}
	}
	if sel[".log"] || sel[".thread.scroll"] {
		t.Error("in-page panes (log, thread) should still hand the wheel to the page at their ends")
	}

	// Fonts: each OS's CJK face and colour emoji in both UI stacks; mono with a face per OS,
	// a CJK fallback, and the generic last.
	varOf := func(name string) string {
		m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("%s not defined", name)
		}
		return m[1]
	}
	zh := regexp.MustCompile(`:lang\(zh-CN\) body, html:lang\(zh-CN\) \{ font-family: ([^;]+); \}`).FindStringSubmatch(css)
	if zh == nil {
		t.Fatal("zh-CN font rule missing")
	}
	for name, stack := range map[string]string{"--font": varOf("--font"), "zh-CN": zh[1]} {
		for _, f := range []string{`"PingFang SC"`, `"Microsoft YaHei UI"`, `"Microsoft YaHei"`, `"Noto Sans CJK SC"`, `"WenQuanYi Micro Hei"`, "sans-serif", `"Apple Color Emoji"`, `"Segoe UI Emoji"`, `"Noto Color Emoji"`} {
			if !strings.Contains(stack, f) {
				t.Errorf("%s font stack lacks %s", name, f)
			}
		}
		// emoji faces after the generic, so digits and punctuation never come from them
		if strings.Index(stack, "Emoji") < strings.Index(stack, "sans-serif") {
			t.Errorf("%s: emoji faces must follow sans-serif", name)
		}
	}
	mono := varOf("--mono")
	for _, f := range []string{"ui-monospace", "Menlo", `"Cascadia Mono"`, "Consolas", `"DejaVu Sans Mono"`, `"Noto Sans CJK SC"`} {
		if !strings.Contains(mono, f) {
			t.Errorf("--mono lacks %s", f)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(mono), "monospace") {
		t.Error("--mono must end with the generic monospace")
	}
	// Every explicit font-family list ends in a generic family (or uses the variables).
	for _, m := range regexp.MustCompile(`font-family:\s*([^;}]+)`).FindAllStringSubmatch(css, -1) {
		v := strings.TrimSpace(m[1])
		if strings.HasPrefix(v, "var(") || v == "inherit" {
			continue
		}
		if !strings.Contains(v, "sans-serif") && !strings.Contains(v, "monospace") {
			t.Errorf("font-family without a generic fallback: %s", v)
		}
	}
}
