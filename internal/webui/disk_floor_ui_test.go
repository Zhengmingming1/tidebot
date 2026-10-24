package webui

import (
	"regexp"
	"strings"
	"testing"
)

// TestDiskFloorUI: Settings › Data › Storage has the "keep free at least" choice wired to
// storage.settings, the blocked notice outranks the low-disk one, and a run skipped for a
// full disk is explained in both languages. The page's pattern must match the message the
// core stores ("skipped: disk full: <free>/<minimum>").
func TestDiskFloorUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, s := range []string{
		`r("min_free", T("st.min_free_sub"), sel("st-minfree", "min_free"))`,
		`body.min_free_mb = n`,
		`dk && v.blocked ? '<p class="notice bad"`,
		`T("rd.err_skip_disk"`,
		`var ST_MIN_FREE_MB = [0, 100, 200, 500, 1024];`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	m := regexp.MustCompile(`var dk = /(\^skipped: disk full: [^/]*\\/[^/]*)/\.exec\(msg\)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("disk-full pattern not found in runErr")
	}
	re := regexp.MustCompile(m[1])
	got := re.FindStringSubmatch("skipped: disk full: 314572800/524288000")
	if got == nil || got[1] != "314572800" || got[2] != "524288000" {
		t.Fatalf("pattern %s → %v", m[1], got)
	}
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for _, k := range []string{"st.min_free", "st.min_free_sub", "st.min_free_off", "st.disk_blocked", "st.disk_blocked_tip", "rd.err_skip_disk"} {
		if en[k] == "" || zh[k] == "" {
			t.Errorf("missing i18n %s", k)
		}
	}
	for _, k := range []string{"st.disk_blocked", "rd.err_skip_disk"} {
		if !strings.Contains(en[k], "{f}") || !strings.Contains(zh[k], "{m}") {
			t.Errorf("%s lacks placeholders", k)
		}
	}
}
