package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func hitJoin(p []string) string {
	var b strings.Builder
	for i, s := range p {
		if i%2 == 1 {
			b.WriteString("[" + s + "]")
		} else {
			b.WriteString(s)
		}
	}
	return b.String()
}

// TestFindRunHit: the field order, ASCII-only case folding like SQLite LIKE (byte offsets
// stay put), every hit inside the excerpt marked, whitespace folded, the excerpt starting no
// earlier than the hit's line, cuts on character boundaries with "…".
func TestFindRunHit(t *testing.T) {
	long := strings.Repeat("整理今天的新闻，", 40) + "\r\n然后查 Weather 预报，weather\t好就提醒我。" + strings.Repeat("尾", 200)
	h := findRunHit(runDTO{ID: "r1", Prompt: long, Error: "weather api down"}, "WEATHER")
	if h == nil || h.Field != "prompt" || h.Count != 2 || len(h.Parts) != 5 {
		t.Fatalf("hit %+v", h)
	}
	got := hitJoin(h.Parts)
	if !strings.HasPrefix(got, "…然后查 [Weather] 预报，[weather] 好就提醒我。尾") || !strings.HasSuffix(got, "尾…") {
		t.Fatalf("excerpt %q", got)
	}
	for _, p := range h.Parts {
		if !utf8.ValidString(p) || strings.ContainsAny(p, "\r\n\t") {
			t.Fatalf("part %q", p)
		}
	}

	// mid-line: hitBefore runes of context, cut on a rune boundary
	mid := strings.Repeat("天", 50) + "目标" + strings.Repeat("地", 100)
	h = findRunHit(runDTO{Prompt: mid}, "目标")
	if got := hitJoin(h.Parts); got != "…"+strings.Repeat("天", hitBefore)+"[目标]"+strings.Repeat("地", 80)+"…" {
		t.Fatalf("mid %q", got)
	}

	// a hit straddling the end of the window is kept whole
	edge := "x" + strings.Repeat("a", 80) + "abc" + "zzz"
	h = findRunHit(runDTO{Prompt: "abc" + edge}, "abc")
	if got := hitJoin(h.Parts); !strings.HasPrefix(got, "[abc]x") || h.Count != 2 {
		t.Fatalf("edge %q %d", got, h.Count)
	}

	// only ASCII folds (SQLite LIKE): Ä does not match ä
	if findRunHit(runDTO{Prompt: "Äpfel"}, "äpfel") != nil {
		t.Fatal("non-ASCII folded")
	}
	if h := findRunHit(runDTO{Prompt: "x", Error: "quota EXCEEDED"}, "exceeded"); h == nil || h.Field != "error" || hitJoin(h.Parts) != "quota [EXCEEDED]" {
		t.Fatalf("error %+v", h)
	}
	if h := findRunHit(runDTO{Prompt: "x", TaskName: "Daily brief"}, "brief"); h == nil || h.Field != "task" || h.Parts != nil {
		t.Fatalf("task %+v", h)
	}
	if h := findRunHit(runDTO{ID: "01JABCDEF99", Prompt: "x"}, "def99"); h == nil || h.Field != "id" || hitJoin(h.Parts) != "01JABC[DEF99]" {
		t.Fatalf("id %+v", h)
	}
	if findRunHit(runDTO{Prompt: "x"}, "  ") != nil {
		t.Fatal("blank query")
	}
	if n := countHits(strings.Repeat("a", 500), "a"); n != hitCountMax {
		t.Fatalf("count %d", n)
	}
}

// TestRunsSearchHit: a hit far past the 300-byte preview comes back as an excerpt; the
// prompt stays a preview; % and _ are literal; without search text there is no match field.
func TestRunsSearchHit(t *testing.T) {
	c := login(t)
	long := strings.Repeat("整理今天的新闻，", 40) + "最后检查 100%_done 标记"
	seedRun(t, c, "sh1", long, "succeeded", 0.01, time.Now())
	seedRun(t, c, "sh2", "unrelated", "succeeded", 0.01, time.Now())

	list := listRuns(t, c, "q=100%25_done")
	if len(list) != 1 || list[0]["id"] != "sh1" {
		t.Fatalf("list %v", list)
	}
	p, _ := list[0]["prompt"].(string)
	if len(p) > 300+len("…") || !utf8.ValidString(p) {
		t.Fatalf("prompt not a preview: %d bytes", len(p))
	}
	var m runHit
	b, _ := json.Marshal(list[0]["match"])
	_ = json.Unmarshal(b, &m)
	if m.Field != "prompt" || m.Count != 1 || len(m.Parts) != 3 || m.Parts[1] != "100%_done" || !strings.HasPrefix(m.Parts[0], "…") {
		t.Fatalf("match %+v", m)
	}
	for _, r := range listRuns(t, c, "") {
		if _, ok := r["match"]; ok {
			t.Fatalf("match without q: %v", r)
		}
	}
}

// TestRunsHitUI: the runs table shows the excerpt escaped with marks, the title is marked,
// search boxes wait for the input method, and IME keys never reach our shortcuts.
func TestRunsHitUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, s := range []string{
		`window.addEventListener("keydown", function (e) { if (imeKey(e)) e.stopImmediatePropagation(); }, true);`,
		`imeInput($("#rq"), 250,`,
		`imeInput($("#tq"), 120,`,
		`imeInput($("#lv-q", host), 150,`,
		`imeInput(input, 0, function (v) { palUpdate(v.trim()); });`,
		`runsTable(list, true, v.q)`,
		`hlText(runTitle(r), lq)`,
		`(runHitHtml(r.match) ||`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	for _, bad := range []string{`$("#rq").addEventListener("input"`, `$("#tq").addEventListener("input"`, `$("#lv-q", host).addEventListener("input"`} {
		if strings.Contains(src, bad) {
			t.Errorf("app.js still has %s", bad)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	if !strings.Contains(string(css), ".cell-main small.run-hit {") {
		t.Error("styles.css lacks .run-hit")
	}
	for _, f := range []string{"zh-CN", "en"} {
		raw, _ := os.ReadFile("static/i18n/" + f + ".json")
		var d map[string]string
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"runs.hit_prompt", "runs.hit_error", "runs.hit_task", "runs.hit_id", "runs.hit_tip"} {
			if d[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}

	hit := regexp.MustCompile(`(?s)  function runHitHtml\(m\) \{.*?\n  \}\n`).FindString(src)
	ime := regexp.MustCompile(`(?s)  function imeInput\(el, ms, fn\) \{.*?\n  \}\n`).FindString(src)
	if hit == "" || ime == "" {
		t.Fatal("runHitHtml / imeInput not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/"/g,"&quot;");}
function T(k,v){return k+(v?"|"+v.f+"|"+v.n:"");}
function num(n){return String(n);}
` + hit + ime + `
var fail = function (m) { console.error(m); process.exitCode = 1; };
var h = runHitHtml({ field: "prompt", count: 2, parts: ["…a<b>", "x\"y", "<img src=1>…"] });
if (h.indexOf("<img") >= 0 || h.indexOf("<b>") >= 0 || h.indexOf("<mark>x&quot;y</mark>") < 0 || h.indexOf('class="hit-f">runs.hit_prompt<') < 0) fail(h);
if (runHitHtml(null) !== "" || runHitHtml({ field: "task", count: 1 }) !== "") fail("empty");

// imeInput: fake input element with an event table and fake timers
var timers = [], now = [];
global.setTimeout = function (f) { timers.push(f); return timers.length; };
global.clearTimeout = function (id) { if (id) timers[id - 1] = null; };
var flush = function () { var t = timers; timers = []; t.forEach(function (f) { if (f) f(); }); };
var el = { value: "", on: {}, addEventListener: function (k, f) { this.on[k] = f; } };
var got = [];
imeInput(el, 200, function (v) { got.push(v); });
el.on.compositionstart();
el.value = "tian"; el.on.input({ isComposing: true });
el.value = "tianqi"; el.on.input({ isComposing: true });
flush();
if (got.length) fail("searched while composing: " + got);
el.value = "天气"; el.on.compositionend(); el.on.input({ isComposing: false });
flush();
if (got.join(",") !== "天气") fail("commit: " + got);
el.value = "天气 a"; el.on.input({}); el.value = "天气 ab"; el.on.input({}); flush();
if (got.join(",") !== "天气,天气 ab") fail("debounce: " + got);
el.value = ""; el.on.input({});
if (got[got.length - 1] !== "" || got.length !== 3) fail("clear: " + got);
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
