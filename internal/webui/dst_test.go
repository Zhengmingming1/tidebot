package webui

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The schedule preview says when a clock change moves or merges a fixed daily time.
func TestSchedulerPreviewDSTNote(t *testing.T) {
	c := login(t)
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	c.s.deps.LocFn = func() *time.Location { return ny }
	c.s.deps.Tasks.Sched.SetLocation(ny)
	for _, tc := range []struct{ cron, kind, wall string }{{"30 2 * * *", "gap", "02:30"}, {"0 1 * * *", "fold", "01:00"}} {
		code, out := c.json(http.MethodGet, "/api/scheduler/preview?cron="+strings.ReplaceAll(tc.cron, " ", "+"), nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", tc.cron, code, out)
		}
		d, _ := out["dst"].(map[string]any)
		if d == nil || d["kind"] != tc.kind || d["wall"] != tc.wall || d["date"] == "" {
			t.Fatalf("%s: dst %v", tc.cron, out["dst"])
		}
		at, err := time.Parse(time.RFC3339, d["at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if got := at.In(ny).Format("15:04"); (tc.kind == "gap" && got != "03:00") || (tc.kind == "fold" && got != "01:00") {
			t.Fatalf("%s: runs at %s", tc.cron, got)
		}
	}
	for _, cr := range []string{"0+9+*+*+*", "*/15+*+*+*+*", "0+*+*+*+*"} {
		if _, out := c.json(http.MethodGet, "/api/scheduler/preview?cron="+cr, nil); out["dst"] != nil {
			t.Fatalf("%s: unexpected dst %v", cr, out["dst"])
		}
	}
	c.s.deps.LocFn = func() *time.Location { return time.UTC }
	c.s.deps.Tasks.Sched.SetLocation(time.UTC)
	if _, out := c.json(http.MethodGet, "/api/scheduler/preview?cron=30+2+*+*+*", nil); out["dst"] != nil {
		t.Fatalf("UTC: unexpected dst %v", out["dst"])
	}
}

// Budget estimates count a fixed daily time once even across a clock change.
func TestRunsPerDayDST(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	if n := runsPerDay("0 1 * * *", ny); n != 1 {
		t.Fatalf("daily: %d", n)
	}
	if n := runsPerDay("0 * * * *", ny); n < 23 || n > 25 {
		t.Fatalf("hourly: %d", n)
	}
}

func TestSchedulerDSTUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, s := range []string{
		`dstEl.innerHTML = dstNoteHtml(d.dst, d.scheduler_timezone || tz);`,
		`<ul class="next"></ul><p class="sp-dst hidden"></p>`,
		`previewCache[key] = { times: d.times || [], dst: d.dst || null, scheduler_timezone: d.scheduler_timezone };`,
		`(d.dst ? "\n" + dstNoteText(d.dst, d.scheduler_timezone) : "")`,
		`esc(text) + "</span>" + tip(T("se.dst_tip"`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".sched-prev .sp-dst {") {
		t.Error("styles.css lacks .sp-dst")
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := os.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"se.dst_gap", "se.dst_skip", "se.dst_fold", "se.dst_tip"} {
			if !strings.Contains(m[k], "{") {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
