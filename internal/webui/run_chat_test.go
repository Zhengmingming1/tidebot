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
	for _, want := range []string{
		`api("/api/runs/" + enc(id) + "/undo-check", { fresh: true })`,
		`thread.addEventListener("pointerover", prefetch);`,
		`thread.addEventListener("focusin", prefetch);`,
		`rcRestoreUndoCheck(next, t);`,
		`return runUndoChanges(r, redo, btn, x || {});`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("run-chat.js lacks %q", want)
		}
	}
	// The session poll must not start undo-check itself; only hover / focus / click do.
	loadAt := strings.Index(src, "var load = function () {")
	if loadAt < 0 {
		t.Fatal("bindChat load not found")
	}
	load := src[loadAt : loadAt+1800]
	if strings.Contains(load, "undo-check") {
		t.Error("session poll load() calls undo-check")
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
function plural(n, one, many) { return T(n === 1 ? one : many, { n: n }); }
function usd(v, d) { return "$" + Number(v || 0).toFixed(d); }
function when(x) { return "@" + x; }
function dur(ms) { return Math.round(ms / 1000) + "s"; }
function skeleton() { return "<sk>"; }
function statusPill(s) { return "<pill:" + s + ">"; }
function runErr(m) { return "E:" + m; }
function runErrText(info, raw) { return runErr(raw); }
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

var chg = rcTurnHtml({ index: 5, run_id: "run_e", message: "m", status: "succeeded", answer: "ok", changes: { files: 2, add: 3, del: 1 } });
ok(chg.indexOf("chg-badge") > 0 && chg.indexOf("data-chg-jump") > 0 && chg.indexOf("+3") > 0 && chg.indexOf("−1") > 0 && chg.indexOf('href="#/runs/run_e"') > 0, "finished turn with changes: badge and run link");
ok(chg.indexOf('data-rc-undo="undo"') > 0 && chg.indexOf("rd.chg_undo") > 0 && chg.indexOf(" disabled") < 0, "finished turn with changes: undo button");
ok(chg.indexOf("<b>") < 0, "change badge has no raw tags");
var undone = rcTurnHtml({ index: 6, run_id: "run_f", message: "m", status: "succeeded", answer: "ok", changes: { files: 1, add: 1, del: 0, undone: true } });
ok(undone.indexOf("chg-badge undone") > 0 && undone.indexOf("runs.chg_undone_tip") > 0 && undone.indexOf('data-rc-undo="redo"') > 0, "undone turn: dashed badge and put-back");
var part = rcTurnHtml({ index: 7, run_id: "run_g", message: "m", status: "succeeded", answer: "ok", changes: { files: 3, add: 2, del: 2, undone_n: 1 } });
ok(part.indexOf("chg-badge partly") > 0 && part.indexOf("runs.chg_partly_tip") > 0 && part.indexOf("rd.chg_undo_rest") > 0, "partly undone turn: rest button");
var cut = rcTurnHtml({ index: 8, run_id: "run_h", message: "m", status: "succeeded", answer: "ok", changes: { files: 1, add: 1, del: 0, patch_cut: true } });
ok(cut.indexOf(" data-rc-undo") > 0 && cut.indexOf(" disabled") > 0 && cut.indexOf("rd.undo_off_cut") > 0, "cut patch: undo offered but disabled");
ok(rcTurnHtml({ index: 9, run_id: "run_i", message: "m", status: "succeeded", answer: "ok", changes: { files: 0 } }).indexOf("chg-badge") < 0, "zero files: no badge");
ok(rcTurnHtml({ index: 10, run_id: "run_j", message: "m", status: "running", changes: { files: 2, add: 1, del: 0 } }).indexOf("chg-badge") < 0, "running turn: no badge yet");
var odd = rcTurnChgHtml({ run_id: "bad id\"", changes: { files: 1, add: 1, del: 0 } });
ok(odd.indexOf("<a ") < 0 && odd.indexOf("chg-badge") > 0 && odd.indexOf("data-rc-undo") < 0, "odd run id: badge is not a link, no undo");
ok(odd.indexOf("href=") < 0, "odd run id badge has no href");

var effHigh = rcTurnHtml({ index: 11, run_id: "run_k", message: "m", status: "succeeded", answer: "ok", effort: "high", created_at: "t" });
ok(/class="rc-cost num" data-tip="[^"]*\nrc\.turn_effort\{&quot;e&quot;:&quot;tf\.effort_high&quot;\}\nrc\.turn_model\{/.test(effHigh), "finished turn: its own effort in the cost tip");
ok(rcTurnHtml({ index: 12, run_id: "run_l", message: "m", status: "succeeded", answer: "ok" }).indexOf("tf.effort_auto") > 0, "no effort: default in the tip");
var effOdd = rcTurnHtml({ index: 13, run_id: "run_m", message: "m", status: "succeeded", answer: "ok", effort: "max\"><b>" });
ok(effOdd.indexOf("tf.effort_auto") > 0 && effOdd.indexOf("<b>") < 0 && effOdd.indexOf("max") < 0, "odd effort: default, not echoed");
ok(/class="rc-act" data-tip="rc\.turn_effort\{&quot;e&quot;:&quot;tf\.effort_low&quot;\}\nrc\.turn_model\{/.test(rcTurnHtml({ index: 14, run_id: "run_n", message: "m", status: "running", effort: "low" })), "running turn: effort on the answering label");
ok(rcTurnHtml({ index: 15, run_id: "run_o", message: "m", status: "" }).indexOf("rc.turn_effort") < 0, "gone run: no effort claimed");

var mdl = rcTurnHtml({ index: 16, run_id: "run_p", message: "m", status: "succeeded", answer: "ok", provider: "kimi", model: "kimi-k2", created_at: "t" });
ok(/class="rc-cost num" data-tip="[^"]*rc\.turn_model\{&quot;m&quot;:&quot;kimi-k2&quot;,&quot;p&quot;:&quot;kimi&quot;\}"/.test(mdl), "finished turn: its own provider and model in the cost tip");
ok(rcTurnHtml({ index: 17, run_id: "run_q", message: "m", status: "succeeded", answer: "ok", provider: "kimi" }).indexOf("rc.turn_model{&quot;m&quot;:&quot;rc.model_default&quot;,&quot;p&quot;:&quot;kimi&quot;}") > 0, "no model: provider default");
var mdlOdd = rcTurnHtml({ index: 18, run_id: "run_r", message: "m", status: "running", provider: "<i>p</i>", model: "m\"><b>x</b>" });
ok(mdlOdd.indexOf("<b>") < 0 && mdlOdd.indexOf("<i>") < 0 && mdlOdd.indexOf("&lt;b&gt;") > 0, "odd provider/model escaped in the answering tip");
ok(rcTurnHtml({ index: 19, run_id: "run_s", message: "m", status: "" }).indexOf("rc.turn_model") < 0, "gone run: no model claimed");

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

// TestRunChatTurnTipsNotClipped: the answer card must not clip its head's
// tooltips (cost with the turn's effort, copy, undo …). The card used to have
// overflow: hidden, so every tip opening upwards was cut to a sliver; the last
// child now rounds its own corners, and tips right of the spacer open leftwards.
func TestRunChatTurnTipsNotClipped(t *testing.T) {
	css := cssSource(t)
	m := regexp.MustCompile(`(?m)^\.rc-a \{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatal("no .rc-a rule")
	}
	if strings.Contains(m[1], "overflow") {
		t.Errorf(".rc-a clips its tooltips: %q", m[1])
	}
	for _, want := range []string{
		".rc-a > .rc-live:last-child, .rc-a > .rc-a-body:last-child, .rc-a > .rc-a-err:last-child { border-radius: 0 0 11px 11px; }",
		".rc-a-head .rc-sp ~ [data-tip]:hover::after, .rc-a-head .rc-sp ~ [data-tip]:focus-visible::after { left: auto; right: 0; transform: none; }",
		".rc-a-head [data-tip]:hover::after, .rc-a-head [data-tip]:focus-visible::after { top: calc(100% + 6px); bottom: auto; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		var d map[string]string
		raw, _ := os.ReadFile(f)
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(d["rc.turn_effort"], "{e}") {
			t.Errorf("%s: rc.turn_effort = %q", f, d["rc.turn_effort"])
		}
		if m := d["rc.turn_model"]; !strings.Contains(m, "{m}") || !strings.Contains(m, "{p}") || d["rc.model_default"] == "" {
			t.Errorf("%s: rc.turn_model = %q, rc.model_default = %q", f, m, d["rc.model_default"])
		}
	}
}

// TestRunDetailEmbedsChat: GET /api/runs/{id}?chat=1 brings the session a finished run
// starts (as GET /api/chat-sessions/{id} answers it), so the run page draws the thread
// without a second round trip; later turns, unfinished runs, runs outside a session, a
// failed read and requests without chat=1 carry none.
func TestRunDetailEmbedsChat(t *testing.T) {
	c := login(t)
	fc := &fakeChats{}
	c.s.deps.ChatSessions = fc
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_o','succeeded','p',?)`,
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_t','succeeded','next?',?)`,
		`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_x','succeeded','other',?)`,
	} {
		if _, err := c.s.deps.DB.Exec(q, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.s.deps.DB.Exec(`INSERT INTO chat_sessions(id,origin_run_id,created_at,updated_at) VALUES('chat_1','run_o',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for i, rid := range []string{"run_o", "run_t"} {
		if _, err := c.s.deps.DB.Exec(`INSERT INTO chat_session_turns(session_id,turn_index,run_id,created_at) VALUES('chat_1',?,?,?)`, i, rid, now); err != nil {
			t.Fatal(err)
		}
	}
	chat := func(path string) map[string]any {
		t.Helper()
		code, out := c.json(http.MethodGet, path, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", path, code, out)
		}
		if _, ok := out["chat"]; !ok {
			return nil
		}
		m, _ := out["chat"].(map[string]any)
		if m == nil {
			t.Fatalf("%s: chat %v", path, out["chat"])
		}
		return m
	}
	// same body as the session endpoint
	_, direct := c.json(http.MethodGet, "/api/chat-sessions/chat_1", nil)
	got := chat("/api/runs/run_o?chat=1")
	if got == nil || got["id"] != "chat_1" {
		t.Fatalf("origin run: %v", got)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(direct)
	if string(a) != string(b) {
		t.Fatalf("embedded session differs:\n%s\n%s", a, b)
	}
	if tl, _ := got["turn_list"].([]any); len(tl) != 1 {
		t.Fatalf("turn_list %v", got["turn_list"])
	}
	for _, p := range []string{"/api/runs/run_o", "/api/runs/run_o?chat=0", "/api/runs/run_t?chat=1", "/api/runs/run_x?chat=1"} {
		if m := chat(p); m != nil {
			t.Errorf("%s carries chat %v", p, m)
		}
	}
	for _, st := range []string{"running", "queued"} {
		if _, err := c.s.deps.DB.Exec(`UPDATE runs SET status=? WHERE id='run_o'`, st); err != nil {
			t.Fatal(err)
		}
		if m := chat("/api/runs/run_o?chat=1"); m != nil {
			t.Errorf("%s run carries chat %v", st, m)
		}
	}
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET status='failed' WHERE id='run_o'`); err != nil {
		t.Fatal(err)
	}
	if m := chat("/api/runs/run_o?chat=1"); m == nil {
		t.Error("failed run: no chat")
	}
	fc.err = ErrChatSessionNotFound // the page then asks for it itself
	if m := chat("/api/runs/run_o?chat=1"); m != nil {
		t.Errorf("failed read carries chat %v", m)
	}
	fc.err = nil
	c.s.deps.ChatSessions = nil
	if m := chat("/api/runs/run_o?chat=1"); m != nil {
		t.Errorf("unwired carries chat %v", m)
	}

	// The page: the session that came with the run is used as is (no request); a run without
	// it (older server, failed read) still asks; later turns and live runs never do.
	src := appJS(t)
	for _, want := range []string{
		`var binds = api(runBindingsPath(id), { fresh: true });`,
		`return api(runDetailPath(id)).then(runChatPreload).then(function (r) {`,
		`capLoadRunBindings(id, $("#rd-inspector"), binds);`,
		`case "runs": return id ? [pfGet(runDetailPath(id))] : [];`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	rs, err := os.ReadFile("src/run-chat.js")
	if err != nil {
		t.Fatal(err)
	}
	rc := strings.ReplaceAll(string(rs), "\r\n", "\n")
	pre := cut(t, rc, "  function rcDone(st) {", "  function rcBusy(st) {") + cut(t, rc, "  function runChatPreload(r) {", "  function rcTurns(s) {")
	lv, err := os.ReadFile("src/log-view.js")
	if err != nil {
		t.Fatal(err)
	}
	paths := cut(t, strings.ReplaceAll(string(lv), "\r\n", "\n"), "  function runDetailPath(id) {", "  function renderRunDetail(id) {")
	runNode(t, `
var calls = [], bad = [];
function enc(s) { return encodeURIComponent(s); }
function api(p, o) { calls.push(p + (o && o.fresh ? " fresh" : "")); return Promise.resolve({ id: "c1", turn_list: [{ index: 1 }] }); }
`+pre+paths+`
(async function () {
  if (runDetailPath("r 1") !== "/api/runs/r%201?chat=1") bad.push("path " + runDetailPath("r 1"));
  if (runBindingsPath("r 1") !== "/api/capabilities/run-bindings?run_id=r%201") bad.push("bindings path");
  var r = await runChatPreload({ status: "succeeded", chat_session: { id: "c1", turn: 0 }, chat: { id: "c1", turn_list: [] } });
  if (calls.length || !r._chat || r._chat.id !== "c1" || "chat" in r) bad.push("embedded: " + calls + " " + JSON.stringify(r));
  r = await runChatPreload({ status: "failed", chat_session: { id: "c1", turn: 0 } });
  if (calls.join() !== "/api/chat-sessions/c1?caps=1 fresh" || !r._chat) bad.push("fallback: " + calls);
  calls = [];
  r = await runChatPreload({ status: "succeeded", chat_session: { id: "c1", turn: 0 }, chat: { id: "other" } });
  if (calls.join() !== "/api/chat-sessions/c1?caps=1 fresh") bad.push("mismatched id used: " + calls);
  calls = [];
  await runChatPreload({ status: "succeeded", chat_session: { id: "c1", turn: 2 } });
  await runChatPreload({ status: "running", chat_session: { id: "c1", turn: 0 } });
  await runChatPreload({ status: "succeeded" });
  if (calls.length) bad.push("asked when it shouldn't: " + calls);
  if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
})();
`, nil)
}
