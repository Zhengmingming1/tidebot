package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestTaskPauseUI: a task's Pause button opens the "pause for…" menu (shared with "pause
// all", no native dialog), a snoozed task says when it resumes, and while every schedule is
// paused the task list / task page mark the next run the pause holds back (escaped, and
// repainted when the pause changes).
func TestTaskPauseUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function pzMenu(anchor, dir, o) {`,
		`function taskPauseMenu(anchor, t, done) {`,
		`if (act === "pause") { e.stopPropagation(); taskPauseMenu(tg, t, reload); return; }`,
		`rpc("task.pause_for", { id: t.id, "for": f })`,
		`"<small>" + heldNext(t.next_run_at) + "</small>"`,
		`'<span class="muted">' + heldNext(ts[0]) + "</span>"`,
		`spAdopt(st.sched, asked); paintCC(); paintHeldNext();`,
		`paintHomePause();
    paintHeldNext();`,
		`else if (act === "resume" && k === "snooze") toast(T("tpz.t_resumed"));`,
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
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(f, err)
		}
		for _, k := range []string{"pz.pill_snooze", "pz.why_snooze", "tpz.title", "tpz.no_next", "tpz.note", "tpz.note_nocron", "tpz.wakes", "tpz.t_until", "tpz.t_manual", "tpz.t_resumed"} {
			if strings.TrimSpace(m[k]) == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".nx.held {") {
		t.Error("styles.css lacks .nx.held")
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
var state = { dict: {} };
` + cut(t, src, "  var PAUSE_KINDS", "  // Task page: an automatic pause") +
		cut(t, block, "  var SPZ_FOR", "  function spApply(v) {") +
		cut(t, block, "  function heldNext(iso) {", "  // taskPauseMenu:") + `
var soon = new Date(Date.now() + 3600e3).toISOString(), later = new Date(Date.now() + 5 * 3600e3).toISOString();
// not paused: a plain "next"
if (/held/.test(heldNext(soon)) || heldNext(soon).indexOf('data-next="' + soon + '"') < 0) bad.push("held while running: " + heldNext(soon));
// paused for 2 hours: the run in 1 hour is held, the one in 5 hours is not
spAdopt({ paused: true, until: new Date(Date.now() + 2 * 3600e3).toISOString() }, 100);
if (!spHolds(soon) || heldNext(soon).indexOf("spz.held") < 0 || heldNext(soon).indexOf("data-tip=") < 0) bad.push("not held: " + heldNext(soon));
if (spHolds(later)) bad.push("held after the end");
// paused until resumed: everything is held
spAdopt({ paused: true, until: "" }, 200);
if (!spHolds(later)) bad.push("manual pause doesn't hold");
if (/<x>/.test(heldNext('<x>"'))) bad.push("next not escaped");
// a snoozed task: its own pill and tooltip with the end
var t = { status: "paused", pause_kind: "snooze", pause_detail: "2026-10-27T01:00:00Z", paused_at: "2026-10-26T12:00:00Z" };
var p = taskPill("paused", t);
if (p.indexOf("pz.pill_snooze") < 0 || p.indexOf("pz.why_snooze") < 0 || p.indexOf("W(2026-10-27T01:00:00Z)") < 0) bad.push("snooze pill: " + p);
t.pause_detail = '<x>"';
if (/<x>/.test(taskPill("paused", t))) bad.push("snooze pill not escaped");
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
