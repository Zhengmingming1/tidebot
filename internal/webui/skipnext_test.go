package webui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// A skipped run: the task answer lists it, Home's "next 24 hours" keeps it (marked
// skipped) so it can be put back.
func TestSkipNextAPI(t *testing.T) {
	s, _ := testServer(t)
	ctx := context.Background()
	tv, err := s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: "hourly", Prompt: "p", CronExpr: "0 * * * *", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.deps.Tasks.SkipRun(ctx, tv.Task.ID, time.Time{}, false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.deps.Tasks.Get(ctx, tv.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := toTaskDTO(view)
	if len(d.SkipRuns) != 1 || d.SkipRuns[0] != res.At || d.NextRunAt != res.NextRunAt {
		t.Fatalf("dto %+v / %+v", d, res)
	}
	items, err := s.upcoming(ctx, 24*time.Hour, 12)
	if err != nil {
		t.Fatal(err)
	}
	var skipped, kept int
	for _, it := range items {
		if it.TaskID != tv.Task.ID {
			continue
		}
		if it.Skipped {
			skipped++
			if it.At != res.At {
				t.Fatalf("wrong run marked skipped: %+v", it)
			}
		} else {
			kept++
		}
	}
	if skipped != 1 || kept < 1 {
		t.Fatalf("upcoming %+v", items)
	}
	raw, _ := json.Marshal(items[0])
	if !strings.Contains(string(raw), `"skipped":true`) && items[0].Skipped {
		t.Fatalf("json %s", raw)
	}
	// a task with nothing skipped: no field in the answer
	tv2, _ := s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: "plain", Prompt: "p", CronExpr: "0 * * * *", Actor: "user"})
	v2, _ := s.deps.Tasks.Get(ctx, tv2.Task.ID)
	if b, _ := json.Marshal(toTaskDTO(v2)); strings.Contains(string(b), "skip_runs") {
		t.Fatalf("empty skip_runs sent: %s", b)
	}
}

func TestSkipNextUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`rpc("task.skip_run", { id: t.id, at: at, undo: !!undo })`,
		`skipAt: nx,`,
		`if (f === "skip") { skipRun(t, nx, false, done); return; }`,
		`d.next.slice(0, 4).map(next24Row).join("")`,
		`wireNext24(d.next);`,
		`heldNext(ts[0]) + "</span>" + skipChip(t)`,
		`if (!head) wireSkipChip(t, reload);`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	block := cut(t, src, "  // ---------- skip one run ----------", "  // Home › next 24 hours: the card's pause button,")
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "innerHTML ="} {
		if strings.Contains(block, bad) {
			t.Errorf("skip block uses %s", bad)
		}
	}
	keys := map[string]bool{}
	for _, m := range regexpAll(src, `T\("(skip\.[a-z_]+)"`) {
		keys[m] = true
	}
	for _, m := range regexpAll(src, `T\(s\.skipped \? "(skip\.[a-z_]+)" : "(skip\.[a-z_]+)"\)`) {
		keys[m] = true
	}
	if len(keys) < 15 {
		t.Fatalf("found only %d skip keys", len(keys))
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
	for _, want := range []string{".timeline.tl-sk li {", ".timeline li.skipped .tl-time", "@media (hover: none) { .tl-skip { opacity: 1; } }", ".skip-chip {"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %s", want)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `
var bad = [];
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function when(iso) { return "W(" + iso + ")"; }
function clock(iso) { return "C(" + iso + ")"; }
function relDay() { return "today"; }
function cronHuman(c) { return "every " + c; }
function enc(s) { return encodeURIComponent(s); }
function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }
function ic(n) { return "<i " + n + ">"; }
` + block + `
var r = next24Row({ at: "2026-11-02T01:00:00Z", task_id: "task_1", task_name: "Daily", cron: "0 9 * * *" });
if (r.indexOf('class=""') < 0 || r.indexOf("<i skipFwd>") < 0 || r.indexOf("skip.this_tip") < 0 || r.indexOf('data-task="task_1"') < 0) bad.push("row: " + r);
var k = next24Row({ at: "2026-11-02T01:00:00Z", task_id: "task_1", task_name: "Daily", cron: "0 9 * * *", skipped: true });
if (k.indexOf('class="skipped"') < 0 || k.indexOf("<i undo>") < 0 || k.indexOf("skip.skipped") < 0 || k.indexOf("skip.put_back") < 0) bad.push("skipped row: " + k);
var x = next24Row({ at: '"><x>', task_id: '"><x>', task_name: "<x>", cron: "<x>" });
if (/<x>/.test(x)) bad.push("row not escaped: " + x);
if (skipChip({ id: "t", skip_runs: [] }) !== "" || skipChip({ id: "t" }) !== "") bad.push("chip without skips");
var c = skipChip({ id: '"><x>', skip_runs: ["2026-11-02T01:00:00Z", "<x>"] });
if (/<x>/.test(c) || c.indexOf('skip.chip{&quot;n&quot;:2}') < 0 || c.indexOf("W(2026-11-02T01:00:00Z)") < 0) bad.push("chip: " + c);
if (skipErr({ message: "at most 20 upcoming runs can be skipped; pause the task instead" }) !== "skip.e_many") bad.push("e_many");
if (skipErr({ message: "that time is not one of the task's upcoming runs" }) !== "skip.e_time") bad.push("e_time");
if (skipErr({ message: "only an active scheduled task has runs to skip" }) !== "skip.e_sched") bad.push("e_sched");
if (skipErr({ message: "other" }) !== "other") bad.push("other");
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

// regexpAll returns every capture group of every match.
func regexpAll(src, pat string) []string {
	var out []string
	for _, m := range regexp.MustCompile(pat).FindAllStringSubmatch(src, -1) {
		out = append(out, m[1:]...)
	}
	return out
}
