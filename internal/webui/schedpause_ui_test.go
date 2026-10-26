package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestSchedPauseUI: "pause all schedules" is reachable from the sidebar (every page), Home's
// next-24-hours card and the command palette; it never uses a native dialog; every string is
// in both languages; the dashboard carries the state; and (under node) a status answer that
// left before a change can't undo it, while Home dims the runs the pause holds back.
func TestSchedPauseUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`box.innerHTML = spPillHtml() + '<button class="cc-card cc-'`,
		`spAdopt(st.sched, asked); paintCC();`,
		`id="spz-home" aria-haspopup="menu"`,
		`<div id="spz-banner"></div><div id="next24"></div>`,
		`return '<li data-at="' + esc(s.at) + '">`,
		`rpc("scheduler.pause.set", on ? { paused: true, "for": choice } : { paused: false })`,
		`api("/api/rpc/scheduler.pause.get", { method: "POST", body: {}, read: true })`,
		`var SPZ_FOR = ["1h", "4h", "tomorrow", "manual"];`,
		`label: T("spz.pal_resume")`, `label: T("spz.pal_pause")`, `label: T("spz.pal_pause_manual")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	block := cut(t, src, "  // ---------- pause all schedules ----------", "  // ---------- tab title ----------")
	for _, bad := range []string{"confirm(", "alert(", "prompt("} {
		if strings.Contains(block, bad) {
			t.Errorf("pause block uses %s", bad)
		}
	}
	// every key used exists in both languages (spz.f_<choice> through SPZ_FOR)
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(spz\.[a-z_]+)"`).FindAllStringSubmatch(src, -1) {
		if !strings.HasSuffix(m[1], "_") {
			keys[m[1]] = true
		}
	}
	for _, f := range []string{"1h", "4h", "tomorrow", "manual"} {
		keys["spz.f_"+f] = true
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
	for _, want := range []string{".spz-pill {", ".spz-menu { position: fixed;", ".timeline li.held", "@media (max-width: 960px) { .spz-pill"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %s", want)
		}
	}

	// dashboard carries the state when the core provides it
	c := login(t)
	c.s.deps.SchedPause = func(context.Context) any {
		return map[string]any{"paused": true, "until": "2026-10-26T06:00:00Z", "due": 2}
	}
	code, out := c.json(http.MethodGet, "/api/dashboard", nil)
	if sp, _ := out["sched_pause"].(map[string]any); code != 200 || sp["paused"] != true || sp["due"] != float64(2) {
		t.Fatalf("dashboard %d %v", code, out["sched_pause"])
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `
var bad = [];
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function when(iso) { return "W(" + iso + ")"; }
function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;"); }
function ic(n) { return "<i " + n + ">"; }
` + cut(t, block, "  var SPZ_FOR", "  function spApply(v) {") + `
// newer answers win, older ones are ignored, and only real changes report true
if (!spAdopt({ paused: true, until: "U1", due: 0 }, 100)) bad.push("first answer not adopted");
if (spAdopt({ paused: false }, 50)) bad.push("older answer adopted");
if (!spState.paused) bad.push("older answer undid the pause");
if (spAdopt({ paused: true, until: "U1", due: 0 }, 200)) bad.push("same state reported as a change");
if (!spAdopt({ paused: false }, 300) || spState.paused) bad.push("resume not adopted");
if (spAdopt(null, 400)) bad.push("empty answer adopted");
// pill only while paused; manual vs timed text; tooltip carries since / due on lines
if (spPillHtml() !== "") bad.push("pill while running");
spAdopt({ paused: true, until: "", since: "S", due: 3 }, 500);
var h = spPillHtml();
if (h.indexOf("spz.pill_manual") < 0 || h.indexOf('aria-haspopup="menu"') < 0) bad.push("manual pill: " + h);
var tip = spTip(spState);
if (tip.split("\n").length !== 3 || tip.indexOf("spz.ends_manual") < 0 || tip.indexOf('spz.note_due{"n":3}') < 0) bad.push("tip: " + tip);
spAdopt({ paused: true, until: "2026-10-26T06:00:00Z", since: "S", due: 0 }, 600);
if (spPillHtml().indexOf("spz.pill_until") < 0 || spTip(spState).split("\n").length !== 2) bad.push("timed pill");
spAdopt({ paused: true, until: "<x>\"", due: 0 }, 700);
if (/<x>/.test(spPillHtml())) bad.push("pill not escaped");
console.log(JSON.stringify(bad));
`
	cmd := exec.Command(node, "-e", script)
	outb, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, outb)
	}
	var fails []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(outb))), &fails); err != nil {
		t.Fatalf("node output %s", outb)
	}
	for _, f := range fails {
		t.Error(f)
	}
}
