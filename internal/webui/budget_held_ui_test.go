package webui

import (
	"regexp"
	"strings"
	"testing"
)

// A run skipped because runs already going hold what is left of a budget is
// explained in the viewer's language: run page / pill tooltip, spend events,
// notification center; the budget rows and "remaining" show what is held.
func TestBudgetHeldUI(t *testing.T) {
	const reason = "global/ daily USD cap held by 2 running (0.2000+0.8000/1.0000)"
	c, u, l := parseBudgetDetail(reason)
	h, n := parseHeldDetail(reason)
	if c != "held" || u != "$0.2000" || l != "$1.0000" || h != "$0.8000" || n != "2" {
		t.Fatal(c, u, l, h, n)
	}
	if h, n := parseHeldDetail("task/x daily CNY cap held by 1 running (1.0000+0.5000/2.0000)"); h != "¥0.5000" || n != "1" {
		t.Fatal(h, n)
	}
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, s := range []string{
		`"rd.err_skip_held" : "rd.err_skip_budget"`,
		`T("be.held", {`,
		`budgetWhy(n.body || "")`,
		`held: e.held, n: e.n`,
		`'<span class="held" data-w="' + hp + '"></span>'`,
		`T("sp.remaining_held_tip"`,
		`global.max_usd - s.total_usd - (global.held_usd || 0)`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	m := regexp.MustCompile(`m = /(daily \(USD\|CNY\) cap held by [^\n]*?)/\.exec\(d\);`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("held pattern not found in budgetWhy")
	}
	got := regexp.MustCompile(m[1]).FindStringSubmatch(reason)
	if got == nil || got[2] != "2" || got[3] != "0.2000" || got[4] != "0.8000" || got[5] != "1.0000" {
		t.Fatalf("pattern %s → %v", m[1], got)
	}
	css, _ := Static.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".progress > span.held") || !strings.Contains(string(css), ".progress.split") {
		t.Error("held segment style missing")
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for _, k := range []string{"be.held", "rd.err_skip_held", "notif.budget_held", "notif.budget_cap", "act.skip", "sp.held_tip", "sp.remaining_held_tip"} {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
	for _, d := range []map[string]string{en, zh} {
		for _, p := range []string{"{name}", "{n}", "{used}", "{held}", "{cap}"} {
			if !strings.Contains(d["be.held"], p) {
				t.Errorf("be.held lacks %s", p)
			}
		}
		if !strings.Contains(d["rd.err_skip_held"], "{why}") || !strings.Contains(d["sp.held_tip"], "{held}") {
			t.Error("placeholders missing")
		}
	}
}
