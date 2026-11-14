package webui

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestRunDetailChatSession: the run page learns which session a run belongs to (turn 0
// for the run a session continues, n for a later turn) so it can show the thread or link
// back to it; runs outside any session have no chat_session.
func TestRunDetailChatSession(t *testing.T) {
	c := login(t)
	c.s.deps.ChatSessions = &fakeChats{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_o','succeeded','p',?)`,
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_t','succeeded','next?',?)`,
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_x','succeeded','other',?)`,
		`INSERT INTO chat_sessions(id,origin_run_id,created_at,updated_at) VALUES('chat_1','run_o',?,?)`,
	} {
		args := []any{now}
		if strings.Contains(q, "updated_at") {
			args = append(args, now)
		}
		if _, err := c.s.deps.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i, rid := range []string{"run_o", "run_t"} {
		if _, err := c.s.deps.DB.Exec(`INSERT INTO chat_session_turns(session_id,turn_index,run_id,created_at) VALUES('chat_1',?,?,?)`, i, rid, now); err != nil {
			t.Fatal(err)
		}
	}
	get := func(id string) map[string]any {
		code, out := c.json(http.MethodGet, "/api/runs/"+id, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", id, code, out)
		}
		cs, _ := out["chat_session"].(map[string]any)
		return cs
	}
	if cs := get("run_o"); cs == nil || cs["id"] != "chat_1" || cs["turn"] != float64(0) || cs["origin_run_id"] != "run_o" {
		t.Fatalf("origin run %v", cs)
	}
	if cs := get("run_t"); cs == nil || cs["turn"] != float64(1) || cs["origin_run_id"] != "run_o" {
		t.Fatalf("turn run %v", cs)
	}
	if cs := get("run_x"); cs != nil {
		t.Fatalf("run outside a session %v", cs)
	}
	c.s.deps.ChatSessions = nil // sessions not wired: nothing to link to
	if cs := get("run_o"); cs != nil {
		t.Fatalf("unwired %v", cs)
	}
}

// TestRunChatUI: the run page's follow-up box and thread (src/run-chat.js) under node with
// stand-ins for the page helpers: when it shows, escaping, the turn states, error codes.
func TestRunChatUI(t *testing.T) {
	b, err := os.ReadFile("src/run-chat.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	app := appJS(t)
	for _, want := range []string{
		`'</span></div><div id="lv"></div></section>' + runChatHtml(r) + '</div>'`,
		"bindRunChat(r);",
		"stopStream(); stopChatStream(); stopPoll();",
		`(turn ? "" : '<button type="button" class="btn sm" id="rd-rerun"'`,
		"runChatOfHtml(r)",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	// No native dialogs; every write goes through api() (CSRF header) as POST.
	if regexp.MustCompile(`\b(window\.)?(confirm|alert|prompt)\(`).MatchString(src) {
		t.Error("run-chat.js uses a native dialog")
	}
	if strings.Contains(src, "fetch(") || strings.Contains(src, "innerHTML = t.") {
		t.Error("run-chat.js bypasses api() or writes server text as HTML")
	}

	// Every stable code the session API answers with has words in both languages.
	goSrc, _ := os.ReadFile("chat_sessions.go")
	codes := map[string]bool{}
	for _, m := range regexp.MustCompile(`"code":\s*"(\w+)"|, "(\w+)"\},`).FindAllStringSubmatch(string(goSrc), -1) {
		codes[m[1]+m[2]] = true
	}
	if len(codes) < 6 {
		t.Fatalf("codes %v", codes)
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		var d map[string]string
		raw, _ := os.ReadFile(f)
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		for code := range codes {
			if d["rc.err_"+code] == "" {
				t.Errorf("%s: no rc.err_%s", f, code)
			}
		}
	}

	runNode(t, `
var state = { dict: { "rc.err_busy": 1, "rc.err_dir": 1, "rn.ws_sensitive_tip": 1, "rn.ws_not_found_tip": 1 } };
function esc(s) { return String(s == null ? "" : s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;"); }
function enc(s) { return encodeURIComponent(s); }
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function ic(n) { return "<i:" + n + ">"; }
function num(n) { return String(n); }
function usd(v, d) { return "$" + Number(v || 0).toFixed(d); }
function when(x) { return "@" + x; }
function dur(ms) { return Math.round(ms / 1000) + "s"; }
function skeleton() { return "<sk>"; }
function statusPill(s) { return "<pill:" + s + ">"; }
function runErr(m) { return "E:" + m; }
function mdHtml(s) { return "<md>" + esc(s) + "</md>"; }
function classifyLog(text) { var k = /^assistant: tool /.test(text) ? "tool" : /^assistant: /.test(text) ? "text" : "sys"; return { ts: "", kind: k, text: text }; }
`+src+`
var bad = [];
function ok(c, m) { if (!c) bad.push(m); }
var fin = { id: "run_a", status: "succeeded" };
ok(runChatHtml(fin).indexOf('id="rc-form"') > 0 && runChatHtml(fin).indexOf("<sk>") < 0, "finished run without a session: box, no skeleton");
ok(runChatHtml({ id: "a", status: "failed", chat_session: { id: "c", turn: 0 } }).indexOf("<sk>") > 0, "a session to load: skeleton");
["running", "queued", "skipped"].forEach(function (s) { ok(runChatHtml({ id: "a", status: s }) === "", s + " run shows no box"); });
var pre = runChatHtml({ id: "a", status: "succeeded", chat_session: { id: "c", turn: 0 }, _chat: { turn_list: [{ index: 0, run_id: "a" }, { index: 1, run_id: "run_b", message: "q1", status: "succeeded", answer: "x" }] } });
ok(pre.indexOf('data-turn="1"') > 0 && pre.indexOf('data-turn="0"') < 0 && pre.indexOf("<sk>") < 0 && pre.indexOf('id="rc-sep">') > 0, "preloaded turns drawn with the page, the run itself not repeated");
ok(runChatHtml(fin).indexOf('id="rc-sep" hidden') > 0, "no turns yet: no divider");
ok(runChatHtml({ id: "a", status: "succeeded", trigger: "session" }) === "", "a session turn has no box of its own");
ok(runChatHtml({ id: "a", status: "succeeded", chat_session: { id: "c", turn: 2, origin_run_id: "run_o" } }) === "", "later turn: no box");
ok(runChatOfHtml({ chat_session: { id: "c", turn: 2, origin_run_id: "run_o" } }).indexOf('href="#/runs/run_o"') > 0, "later turn links to the run it continues");
ok(runChatOfHtml({ chat_session: { id: "c", turn: 2, origin_run_id: "x\" onclick=\"y" } }) === "", "odd origin id: no link");
ok(runChatOfHtml({ chat_session: { id: "c", turn: 0, origin_run_id: "run_o" } }) === "", "turn 0: no link");

var done = rcTurnHtml({ index: 1, run_id: "run_b", message: "<img src=x onerror=1>", status: "succeeded", answer: "**hi**", cost_usd: 0.01234, created_at: "t1" });
ok(done.indexOf("<img") < 0 && done.indexOf("&lt;img") > 0, "question escaped");
ok(done.indexOf("$0.0123") > 0 && done.indexOf("<md>") > 0 && done.indexOf("data-rc-copy") > 0 && done.indexOf('href="#/runs/run_b"') > 0, "finished turn: cost, answer, copy, run link");
ok(done.indexOf("data-rc-live") < 0 && done.indexOf("data-rc-stop") < 0, "finished turn: no live box or stop");
var run = rcTurnHtml({ index: 2, run_id: "run_c", message: "m", status: "running", created_at: "2026-11-03T10:00:00Z" });
ok(run.indexOf('data-rc-live="run_c"') > 0 && run.indexOf('data-rc-stop="run_c"') > 0 && run.indexOf("rc.answering") > 0 && run.indexOf("rc-cost") < 0, "running turn: live box, stop, no cost");
ok(rcTurnHtml({ index: 2, run_id: "run_c", message: "m", status: "queued" }).indexOf("rc.queued") > 0, "queued turn");
var failed = rcTurnHtml({ index: 3, run_id: "run_d", message: "m", status: "failed", error: "<b>boom</b>", cost_usd: 0 });
ok(failed.indexOf("<pill:failed>") > 0 && failed.indexOf("E:&lt;b&gt;boom") > 0, "failed turn: status and escaped error");
ok(rcTurnHtml({ index: 4, run_id: "bad id\"", message: "m", status: "" }).indexOf("href=") < 0, "gone run, odd id: no link");

ok(rcErrText({ data: { code: "busy" }, message: "x" }) === "rc.err_busy", "busy code translated");
ok(rcErrText({ data: { code: "weird" }, message: "raw" }) === "raw", "unknown code: message as sent");
ok(rcErrText({ message: "net" }) === "net", "no code");
ok(rcErrText({ data: { code: "dir", problem: "sensitive" }, message: "x" }) === 'rc.err_dir{"why":"rn.ws_sensitive_tip"}', "session folder refused: the Run dialog's reason");
ok(rcErrText({ data: { code: "dir", problem: "weird" }, message: "x" }) === 'rc.err_dir{"why":"rn.ws_not_found_tip"}', "unknown folder problem: generic reason");
var line = rcLiveLine('assistant: tool Bash {"command":"ls <x>"}');
ok(line.indexOf("tool-chip") > 0 && line.indexOf("ls &lt;x&gt;") > 0 && line.indexOf("{") < 0, "tool line: chip and command preview, escaped");
ok(rcLiveLine("assistant: <b>t</b>").indexOf("&lt;b&gt;t") > 0, "text line escaped");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, nil)
}
