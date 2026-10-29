package webui

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

func segText(s []ansSeg, changed bool) []string {
	var out []string
	for _, g := range s {
		if g.X == changed {
			out = append(out, g.T)
		}
	}
	return out
}

func TestAnswerDiff(t *testing.T) {
	old := "# 日报\n\n价格 63 元\nBTC 1,299.50 USD\nkeep\nsame tail   \nlast\n"
	cur := "# 日报\n\n价格 66 元\nBTC 1,301.00 USD\nkeep\nnew line\nsame tail\nlast"
	lines, coarse := AnswerDiff(old, cur)
	if coarse {
		t.Fatal("coarse")
	}
	var ops []string
	for _, l := range lines {
		ops = append(ops, l.Op)
	}
	// trailing spaces on "same tail" and the missing final newline are not changes
	if got := strings.Join(ops, ""); got != "  --++ +  " {
		t.Fatalf("ops %q %+v", got, lines)
	}
	if lines[9].Text != "last" || lines[9].A != 7 || lines[9].B != 8 || lines[8].Text != "same tail" {
		t.Fatalf("tail %+v", lines[8:])
	}
	// pairs: 价格 63 元 → 价格 66 元 marks only the number; 1,299.50 is one token
	if d, a := segText(lines[2].Segs, true), segText(lines[4].Segs, true); strings.Join(d, "|") != "63" || strings.Join(a, "|") != "66" {
		t.Fatalf("price marks %v %v (%+v)", d, a, lines[2:6])
	}
	if d := segText(lines[3].Segs, true); strings.Join(d, "|") != "1,299.50" || strings.Join(segText(lines[5].Segs, true), "") != "1,301.00" {
		t.Fatalf("number token %v", d)
	}
	// unpaired added line, and a pair with no word in common: no marks
	if lines[7].Op != "+" || lines[7].Segs != nil {
		t.Fatalf("unpaired %+v", lines[7])
	}
	l2, _ := AnswerDiff("alpha beta", "gamma delta")
	if len(l2) != 2 || l2[0].Segs != nil || l2[1].Segs != nil {
		t.Fatalf("whole line replaced %+v", l2)
	}
	// Chinese: one character changed in a sentence
	l3, _ := AnswerDiff("今天北京晴，气温 20 度", "今天北京多云，气温 20 度")
	if got := strings.Join(segText(l3[1].Segs, true), "|"); got != "多云" {
		t.Fatalf("cjk %q %+v", got, l3)
	}
	if got := strings.Join(segText(l3[0].Segs, true), "|"); got != "晴" {
		t.Fatalf("cjk old %q", got)
	}
	// segments put back together are the line
	for _, l := range append(lines, l3...) {
		if l.Segs == nil {
			continue
		}
		var b strings.Builder
		for _, g := range l.Segs {
			b.WriteString(g.T)
		}
		if b.String() != l.Text {
			t.Fatalf("segs %q != %q", b.String(), l.Text)
		}
	}
	// CRLF answers read the same as LF
	if l4, _ := AnswerDiff("a\r\nb\r\n", "a\nb\n"); len(l4) != 2 || l4[0].Op != " " || l4[1].Op != " " {
		t.Fatalf("crlf %+v", l4)
	}
	// a huge change is shown as replaced, with the common head and tail kept
	var a, b []string
	for i := 0; i < 700; i++ {
		a = append(a, "old "+strings.Repeat("x", i%7))
		b = append(b, "new "+strings.Repeat("y", i%5))
	}
	big, coarse := AnswerDiff("head\n"+strings.Join(a, "\n")+"\ntail", "head\n"+strings.Join(b, "\n")+"\ntail")
	if !coarse || len(big) != 1402 || big[0].Op != " " || big[1].Op != "-" || big[701].Op != "+" || big[1401].Text != "tail" || big[701].B != 2 {
		t.Fatalf("coarse %v %d", coarse, len(big))
	}
	if l5, _ := AnswerDiff("", "x"); len(l5) != 1 || l5[0].Op != "+" {
		t.Fatalf("empty old %+v", l5)
	}
}

// The run detail says which answer it is compared with; /answer-diff returns the diff against
// the task's previous succeeded run that has an answer (failed runs and quick runs skipped).
func TestAnswerDiffAPI(t *testing.T) {
	c := login(t)
	now := time.Now()
	db := c.s.deps.DB
	code, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Digest", "prompt": "digest", "provider": "anthropic", "cron": "0 9 * * *", "enabled": false, "timeout_minutes": 30})
	if code != 201 {
		t.Fatalf("task %d %v", code, tk)
	}
	tid := tk["id"].(string)
	seedRun(t, c, "ad1", "digest", "succeeded", 0.01, now.Add(-3*time.Hour))
	seedRun(t, c, "ad2", "digest", "failed", 0.01, now.Add(-2*time.Hour))
	seedRun(t, c, "ad3", "digest", "succeeded", 0.01, now.Add(-time.Hour))
	seedRun(t, c, "ad4", "quick", "succeeded", 0.01, now)
	for id, text := range map[string]string{"ad1": "price 63\nok\n", "ad2": "boom", "ad3": "price 66\nok\n"} {
		if _, err := db.Exec(`UPDATE runs SET task_id=?, result_text=? WHERE id=?`, tid, text, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE runs SET result_text='hello' WHERE id='ad4'`); err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, map[string]any) {
		rec := c.do(http.MethodGet, path, "", nil)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	var d map[string]any
	_, r3 := get("/api/runs/ad3")
	if p, ok := r3["answer_prev"].(map[string]any); !ok || p["run_id"] != "ad1" || p["at"] != now.Add(-3*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("answer_prev %v", r3["answer_prev"])
	}
	for _, id := range []string{"ad1", "ad4"} {
		if _, m := get("/api/runs/" + id); m["answer_prev"] != nil {
			t.Fatalf("%s has answer_prev", id)
		}
	}
	code, d = get("/api/runs/ad3/answer-diff")
	if code != 200 || d["prev"].(map[string]any)["run_id"] != "ad1" || d["added"] != 1.0 || d["removed"] != 1.0 || d["same"] != false {
		t.Fatalf("diff %d %v", code, d)
	}
	lines := d["lines"].([]any)
	if len(lines) != 3 || lines[0].(map[string]any)["op"] != "-" || lines[2].(map[string]any)["text"] != "ok" {
		t.Fatalf("lines %v", lines)
	}
	if code, d := get("/api/runs/ad1/answer-diff"); code != 200 || d["prev"] != nil || len(d["lines"].([]any)) != 0 {
		t.Fatalf("first %d %v", code, d)
	}
	if code, _ := get("/api/runs/nope/answer-diff"); code != 404 {
		t.Fatalf("missing run %d", code)
	}
	// whitespace only: "same", the diff still shows the re-wrapped lines
	_, _ = db.Exec(`UPDATE runs SET result_text=? WHERE id='ad3'`, "price  63\n\nok")
	if _, d := get("/api/runs/ad3/answer-diff"); d["same"] != true || d["added"] == 0.0 {
		t.Fatalf("ws %v", d)
	}
	if rec := c.do(http.MethodPost, "/api/runs/ad3/answer-diff", "", nil); rec.Code == 200 {
		t.Fatal("POST accepted")
	}
}

func TestAnswerDiffUI(t *testing.T) {
	b, _ := os.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, s := range []string{
		`function ansModes(r) { return r.answer_prev ? ["preview", "source", "diff"] : ["preview", "source"]; }`,
		`return m === "source" || (m === "diff" && r && r.answer_prev) ? m : "preview";`,
		`api("/api/runs/" + enc(r.id) + "/answer-diff")`,
		`'<mark class="dx">' + esc(g.t) + "</mark>"`,
		`runAnswerHtml(r, ansMode(r)) +`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, s := range []string{".rd-ans-diff .diff-sum {", "table.diff tr.ins mark.dx {", ".rd-ans-diff tr.gap .linkish {"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		raw, _ := os.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for k, ph := range map[string]string{"rd.ans_diff": "", "rd.ans_diff_tip": "{t}", "rd.ans_diff_none": "", "rd.ans_diff_prev": "", "rd.ans_diff_prev_tip": "{t}", "rd.ans_diff_same": "", "rd.ans_diff_ws": "", "rd.ans_diff_ws_tip": "", "rd.ans_diff_coarse": "", "rd.ans_diff_coarse_tip": "", "rd.ans_diff_fold": "{n}"} {
			if m[k] == "" || !strings.Contains(m[k], ph) {
				t.Errorf("%s %s = %q", f, k, m[k])
			}
		}
	}
	fn := regexp.MustCompile(`(?s)  var ANS_CTX = 3;\n.*?\n  function liveRunStats`).FindString(src)
	if fn == "" {
		t.Fatal("ansDiffHtml not found")
	}
	fn = strings.TrimSuffix(fn, "  function liveRunStats")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");}
function T(k,v){return k+(v?JSON.stringify(v):"");} function ic(n){return "<i>"+n+"</i>";} function num(n){return String(n);}
function enc(s){return encodeURIComponent(s);} function when(s){return "W"+s;}
` + fn + `
var fail = function (m, h) { console.error(m + "\n" + h); process.exitCode = 1; };
var L = [];
for (var i = 1; i <= 20; i++) L.push({ op: " ", text: "l" + i, a: i, b: i });
L.splice(10, 0, { op: "-", text: "<b>x</b> 1", a: 11, segs: [{ t: "<b>x</b> " }, { t: "1", x: true }] }, { op: "+", text: "y", b: 11 });
var d = { prev: { run_id: "p1", at: "2026-10-29T01:00:00Z" }, added: 1, removed: 1, lines: L };
var r = {};
var h = ansDiffHtml(r, d);
// lines 1..7 fold (7 lines), 8..10 shown, change, 11..13 shown, 14..20 fold
if ((h.match(/data-ans-gap=/g) || []).length !== 2) fail("two folds", h);
if (h.indexOf('data-ans-gap="0"') < 0 || h.indexOf('rd.ans_diff_fold{&quot;n&quot;:&quot;7&quot;}') < 0) fail("first fold", h);
if (h.indexOf("<b>") >= 0 || h.indexOf('&lt;b&gt;x&lt;/b&gt; <mark class="dx">1</mark>') < 0) fail("escaped marks", h);
if (h.indexOf('href="#/runs/p1"') < 0) fail("prev link", h);
r._ansOpen = { 0: true };
h = ansDiffHtml(r, d);
if ((h.match(/data-ans-gap=/g) || []).length !== 1 || h.indexOf(">l1<") < 0) fail("opened fold", h);
// short stretches are not folded
d.lines = L.slice(7, 15);
if (/data-ans-gap/.test(ansDiffHtml({}, d))) fail("short stretch folded", ansDiffHtml({}, d));
if (ansDiffHtml({}, { prev: null }).indexOf("rd.ans_diff_none") < 0) fail("none", "");
if (ansDiffHtml({}, { prev: d.prev, added: 0, removed: 0, lines: [] }).indexOf("rd.ans_diff_same") < 0) fail("same", "");
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("ansDiffHtml: %v\n%s", err, out)
	}
}
