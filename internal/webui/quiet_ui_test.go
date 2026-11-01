package webui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// Home's "next 24 hours": a run inside quiet hours carries when it will go instead.
func TestQuietUpcoming(t *testing.T) {
	s, _ := testServer(t)
	ctx := context.Background()
	if _, err := s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: "hourly", Prompt: "p", CronExpr: "0 * * * *", Actor: "user"}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(90 * time.Minute).Truncate(time.Minute)
	s.deps.QuietAt = func(at time.Time) (bool, time.Time) { return at.Before(end), end }
	items, err := s.upcoming(ctx, 24*time.Hour, 12)
	if err != nil || len(items) < 3 {
		t.Fatalf("%v %+v", err, items)
	}
	var held, free int
	for _, it := range items {
		at, _ := time.Parse(time.RFC3339, it.At)
		if at.Before(end) {
			if it.QuietUntil != end.UTC().Format(time.RFC3339) {
				t.Fatalf("held run %+v", it)
			}
			held++
		} else {
			if it.QuietUntil != "" {
				t.Fatalf("free run marked %+v", it)
			}
			free++
		}
	}
	if held < 1 || free < 1 {
		t.Fatalf("held=%d free=%d", held, free)
	}
	if b, _ := json.Marshal(items[len(items)-1]); strings.Contains(string(b), "quiet_until") {
		t.Fatalf("empty quiet_until sent: %s", b)
	}
}

func TestQuietUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`powerCardHtml() + quietCardHtml() +`,
		`quietWire();`,
		`rpc("scheduler.quiet.set", { on: !!v.on, from: v.from, to: v.to, days: v.days || [] })`,
		`rpc("scheduler.quiet.get", {})`,
		`href="#/settings/general/quiet"`,
		`x.quiet_until || ""`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	block := cut(t, src, "  // ---- Settings › General › Quiet hours ----", "  // ---- notification channels (Settings → Notifications) ----")
	for _, bad := range []string{"confirm(", "alert(", "prompt("} {
		if strings.Contains(block, bad) {
			t.Errorf("quiet block uses %s", bad)
		}
	}
	keys := map[string]bool{}
	for _, m := range regexpAll(src, `"(qh\.[a-z_]+)"`) {
		keys[m] = true
	}
	if len(keys) < 20 {
		t.Fatalf("found only %d qh keys", len(keys))
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(f, err)
		}
		for k := range keys {
			if strings.TrimSpace(m[k]) == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, want := range []string{".qh-days button[aria-pressed=\"true\"]", ".spz-pill.qh-pill {", ".tl-quiet {", ".qh-range input[type=\"time\"]"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %s", want)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	rowBlock := cut(t, src, "  function next24Row(s) {", "  function wireNext24(list) {")
	script := `
var bad = [];
var state = { lang: "en" };
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function when(iso) { return "W(" + iso + ")"; }
function clock(iso) { return "C(" + iso + ")"; }
function relDay() { return "today"; }
function cronHuman(c) { return "every " + c; }
function enc(s) { return encodeURIComponent(s); }
function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }
function ic(n) { return "<i " + n + ">"; }
function tip(t) { return "<tip>"; }
` + block + rowBlock + `
var s1 = quietSummary({ from: "23:00", to: "07:00", days: [] });
if (s1.indexOf("qh.range_next") !== 0 || s1.indexOf("qh.every_day") < 0) bad.push("overnight every day: " + s1);
var s2 = quietSummary({ from: "12:00", to: "14:00", days: [1, 2, 3, 4, 5] });
if (s2.indexOf("qh.range{") !== 0 || s2.indexOf("qh.weekdays") < 0) bad.push("weekdays: " + s2);
if (quietSummary({ from: "01:00", to: "06:00", days: [0, 6] }).indexOf("qh.weekends") < 0) bad.push("weekends");
if (quietSummary({ from: "01:00", to: "06:00", days: [1, 3] }).indexOf('qh.n_days{"n":2}') < 0) bad.push("n days");
var h = quietCardHtml();
if ((h.match(/data-qh-day=/g) || []).length !== 7 || h.indexOf('data-qh-day="1"') > h.indexOf('data-qh-day="0"')) bad.push("seven days, Monday first: " + h);
if (h.indexOf('role="switch"') < 0 || h.indexOf('type="time"') < 0) bad.push("card controls");
var r = next24Row({ at: "2026-11-02T16:00:00Z", task_id: "t", task_name: "Night", cron: "0 0 * * *", quiet_until: "2026-11-02T23:00:00Z" });
if (r.indexOf("tl-quiet") < 0 || r.indexOf('class="quiet"') < 0 || r.indexOf("every 0 0 * * *") < 0 || r.indexOf("C(2026-11-02T23:00:00Z)") < 0 || r.indexOf("W(2026-11-02T23:00:00Z)") < 0) bad.push("quiet row: " + r);
var k = next24Row({ at: "2026-11-02T16:00:00Z", task_id: "t", task_name: "Night", cron: "0 0 * * *", quiet_until: "2026-11-02T23:00:00Z", skipped: true });
if (k.indexOf("tl-quiet") >= 0) bad.push("skipped row says quiet: " + k);
var x = next24Row({ at: "a", task_id: "t", task_name: "n", cron: "c", quiet_until: '"><x>' });
if (/<x>/.test(x)) bad.push("not escaped: " + x);
console.log(JSON.stringify(bad));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var fails []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &fails); err != nil {
		t.Fatalf("node output %s", out)
	}
	for _, f := range fails {
		t.Error(f)
	}
}
