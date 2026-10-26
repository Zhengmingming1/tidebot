package webui

import (
	"strings"
	"testing"
	"time"
)

// TestTimezoneUI: Settings › General's timezone row opens a searchable picker wired to
// timezone.get / timezone.set (no native dialogs), timezone.get is a cache-safe read,
// and /api/settings carries the offset the row shows.
func TestTimezoneUI(t *testing.T) {
	js, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`tzButtonHtml(st.tz_offset)`,
		`$("#tz-open").addEventListener("click"`,
		`rpc("timezone.get", {})`,
		`rpc("timezone.set", { zone: z.id })`,
		`"power.get", "timezone.get",`,
		`role="listbox"`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	i, j := strings.Index(src, "function tzOffset("), strings.Index(src, "// ---- Settings › General › Power ----")
	if i < 0 || j < i {
		t.Fatal("timezone block not found")
	}
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "innerHTML = z.", "+ z.label +", "+ z.id +"} {
		if strings.Contains(src[i:j], bad) {
			t.Errorf("timezone block uses %s", bad)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".tz-open {", "#tz-list {", ".modal.tz-modal {"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
}

func TestTZOffsetMin(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	ny, _ := time.LoadLocation("America/New_York")
	in, _ := time.LoadLocation("Asia/Kolkata")
	jan, jul := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		loc  *time.Location
		at   time.Time
		want int
	}{{sh, jan, 480}, {ny, jan, -300}, {ny, jul, -240}, {in, jul, 330}, {time.UTC, jul, 0}} {
		if got := tzOffsetMin(c.loc, c.at); got != c.want {
			t.Errorf("%s %s: %d want %d", c.loc, c.at.Month(), got, c.want)
		}
	}
}
