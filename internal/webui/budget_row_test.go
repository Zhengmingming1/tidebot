package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func budgetRowOf(t *testing.T, c *client, scope, sid string) (usd float64, tok int64, act string, warn int) {
	t.Helper()
	sum, err := c.s.deps.Cost.Summary(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range sum.Budgets {
		if b.Scope == scope && b.ScopeID == sid && b.Period == "day" {
			return b.MaxUSD, b.MaxTokens, b.Action, b.WarnAtPct
		}
	}
	t.Fatalf("no %s/%s budget in %+v", scope, sid, sum.Budgets)
	return
}

// Saving one field of a budget keeps the others: the cap from Settings or the spend
// page no longer resets "on breach" / "warn at", and changing those keeps the caps.
func TestBudgetPartialUpdate(t *testing.T) {
	c := login(t)
	post := func(v map[string]any) int { code, _ := c.json(http.MethodPost, "/api/budgets", v); return code }
	if code := post(map[string]any{"scope": "global", "period": "day", "max_usd": 5, "max_tokens": 200000, "action": "block_notify", "warn_at_pct": 90}); code != 200 {
		t.Fatal(code)
	}
	if code := post(map[string]any{"scope": "global", "period": "day", "max_usd": 7.5}); code != 200 {
		t.Fatal(code)
	}
	if u, tok, a, w := budgetRowOf(t, c, "global", ""); u != 7.5 || tok != 200000 || a != "block_notify" || w != 90 {
		t.Fatalf("cap change reset others: %v %v %v %v", u, tok, a, w)
	}
	if code := post(map[string]any{"scope": "global", "period": "day", "max_tokens": 0}); code != 200 {
		t.Fatal(code)
	}
	if u, tok, a, _ := budgetRowOf(t, c, "global", ""); u != 7.5 || tok != 0 || a != "block_notify" {
		t.Fatalf("token cap off: %v %v %v", u, tok, a)
	}
	if code := post(map[string]any{"scope": "global", "period": "day", "warn_at_pct": 70}); code != 200 {
		t.Fatal(code)
	}
	if u, _, a, w := budgetRowOf(t, c, "global", ""); u != 7.5 || a != "block_notify" || w != 70 {
		t.Fatalf("warn change: %v %v %v", u, a, w)
	}
	// 0 turns the money cap off; the rest stays
	if code := post(map[string]any{"scope": "global", "period": "day", "max_usd": 0}); code != 200 {
		t.Fatal(code)
	}
	if u, _, a, w := budgetRowOf(t, c, "global", ""); u != 0 || a != "block_notify" || w != 70 {
		t.Fatalf("cap off: %v %v %v", u, a, w)
	}
	// a new bucket budget with only a cap gets the defaults
	if code := post(map[string]any{"scope": "bucket", "scope_id": "nightly", "bucket": "nightly", "period": "day", "max_usd": 1.23456}); code != 200 {
		t.Fatal(code)
	}
	if u, _, a, w := budgetRowOf(t, c, "bucket", "nightly"); u != 1.2346 || a != "pause_notify" || w != 80 {
		t.Fatalf("new bucket: %v %v %v", u, a, w)
	}
	for _, bad := range []map[string]any{
		{"scope": "global", "period": "day", "max_usd": -1},
		{"scope": "global", "period": "day", "max_usd": 2e6},
		{"scope": "global", "period": "day", "max_tokens": -5},
		{"scope": "global", "period": "week", "max_usd": 1},
		{"scope": "global", "period": "day", "warn_at_pct": 120},
		{"scope": "global", "period": "day", "action": "delete_everything"},
		{"scope": "user", "period": "day", "max_usd": 1},
	} {
		if code := post(bad); code != 400 {
			t.Errorf("%v accepted: %d", bad, code)
		}
	}
	if u, _, a, w := budgetRowOf(t, c, "global", ""); u != 0 || a != "block_notify" || w != 70 {
		t.Fatalf("rejected request changed the budget: %v %v %v", u, a, w)
	}
}

// Spend page budget rows: column heads, cap button, in-place editor that asks before
// removing a cap (in-app dialog), only the changed field posted; Settings posts one cap.
func TestBudgetRowsUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`'<div class="bud-grid" role="table"`,
		`<button type="button" class="cap-edit" data-bcap="' + k + '"`,
		`confirmDialog({ title: T("sp.cap_off_title", { name: name })`,
		`postBudget(k, a ? { action: sel.value } : { warn_at_pct: +sel.value })`,
		`postBudget(k, { max_usd: v })`,
		`if (k === "g_usd") req = { scope: "global", period: "day", max_usd: Math.max(0, parseFloat(v) || 0) };`,
		`else if (k === "g_tok") req = { scope: "global", period: "day", max_tokens: Math.max(0, parseInt(v, 10) || 0) };`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	if strings.Contains(src, "budget-act") || strings.Contains(src, "budget-row") {
		t.Error("old budget row markup still in app.js")
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".bud-grid {", ".bud-head, .bud-row { display: contents; }", ".cap-edit::before", ".cap-in.bad", ".sp-events .table td { vertical-align: top; }"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	// the tooltip uses [data-tip]::after; the cap box must not take that pseudo-element
	if strings.Contains(string(css), ".cap-edit::after") || strings.Contains(string(css), ":has(") && strings.Contains(string(css), ".cap-in:has") {
		t.Error("cap button clashes with the tooltip or uses :has()")
	}
	for _, lang := range []string{"en", "zh-CN"} {
		raw, _ := Static.ReadFile("static/i18n/" + lang + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"sp.used_cap", "sp.bud_settings", "sp.no_budgets", "sp.cap_tip", "sp.cap_aria", "sp.cap_bad", "sp.cap_off_title", "sp.cap_off_body", "sp.cap_off_ok", "sp.cap_saved", "sp.cap_off_done", "sp.a_pause", "sp.a_block"} {
			if m[k] == "" {
				t.Errorf("%s.json: missing %s", lang, k)
			}
		}
		if len([]rune(m["sp.a_pause"])) > 18 || len([]rune(m["sp.a_block"])) > 18 {
			t.Errorf("%s: action labels too long for the 160px select", lang)
		}
	}
	fn := regexp.MustCompile(`(?s)  function parseCap\(v\) \{.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("parseCap not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := fn + `
var cases = [["5", 5], ["2.50", 2.5], ["12,5", 12.5], ["$7.25", 7.25], ["¥ 30", 30], [" 1.23456 ", NaN], ["0", 0], ["", null], ["  ", null], ["abc", NaN], ["-3", NaN], ["1e3", NaN], ["12345678", NaN], ["0.0001", 0.0001]];
var bad = cases.filter(function (c) { var r = parseCap(c[0]); return !(Number.isNaN(c[1]) ? Number.isNaN(r) : r === c[1]); });
if (bad.length) { console.log(JSON.stringify(bad)); process.exit(1); }
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("parseCap: %v %s", err, out)
	}
}
