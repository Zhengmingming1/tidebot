package webui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A queued run skipped at its slot is explained in the viewer's language; the
// patterns match what the runner stores ("skipped: <why>").
func TestSkippedRunUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`if (/^skipped: task paused/.test(msg)) return T("rd.err_skip_paused");`,
		`var b = /^skipped: budget blocked: (.*)$/.exec(msg);`,
		`if (b) return T("rd.err_skip_budget", { why: budgetWhy(b[1]) });`,
		`esc(runErr(r.error))`,
		`function budgetWhy(d) {`,
		`statusPill(r.status, r.queue_pos, r.error)`,
		`(st === "skipped" || st === "canceled") && err ? ' data-tip="' + esc(runErr(err))`,
		`T("be.token_cap", { name: name, used: m[1], cap: m[2] })`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, k := range []string{"rd.err_skip_budget", "rd.err_skip_paused", "st.skipped"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		if !strings.Contains(m["rd.err_skip_budget"], "{why}") {
			t.Errorf("%s: rd.err_skip_budget lost {why}", f)
		}
	}
}
