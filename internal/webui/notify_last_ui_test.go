package webui

import (
	"strings"
	"testing"
)

// TestNotifyLastDeliveryUI: each channel row shows how the last send went (state + when),
// the details live in the tooltip, the list test refreshes the row, and every string has
// both languages.
func TestNotifyLastDeliveryUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, s := range []string{
		`function ntLastHtml(c)`,
		`"</small></div>" + ntLastHtml(c) +`,
		`data-ago="' + esc(d.at) + '"`,
		`data-tip="' + esc(tip.join("\n")) + '"`,
		`return Promise.resolve(reload())`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".nt-last {", ".nt-last.bad", ".nt-dot {", ".nt-last[data-tip]:hover::after"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for _, k := range []string{"nt.last_never", "nt.last_never_tip", "nt.last_ok_tip", "nt.last_bad_tip", "nt.last_bad", "nt.last_bad_n", "nt.last_ms", "nt.last_ok", "nt.last_ok_at", "nt.last_ok_never"} {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
	if len([]rune(zh["nt.last_bad_n"])) > 10 || len(en["nt.last_bad_n"]) > 14 {
		t.Error("row label too long for the 1024 px row")
	}
}
