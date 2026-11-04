package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestChatPageUI: the 会话 page (src/chat-page.js) under node: the list, a blank and an
// open session, the folder tag, escaping; plus its wiring in the bundle (nav, route) and
// the shared message box keeping the run page's behaviour.
func TestChatPageUI(t *testing.T) {
	b, err := os.ReadFile("src/chat-page.js")
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := os.ReadFile("src/run-chat.js")
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	app := appJS(t)
	for _, want := range []string{
		`var NAV = [["home", "#/"], ["chat", "#/chat"],`,
		`case "chat": p = renderChat(parts[1]); break;`,
		`create: function () { return api("/api/chat-sessions", { method: "POST", body: { origin: "chat" } }); }`,
		`confirmDialog({ title: T("cs.del_title")`,
		`api("/api/chat-sessions/" + enc(s.id) + "/bindings", { method: "PUT", body: body })`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if regexp.MustCompile(`\b(window\.)?(confirm|alert|prompt)\(`).MatchString(src) {
		t.Error("chat-page.js uses a native dialog")
	}
	if strings.Contains(src, "fetch(") {
		t.Error("chat-page.js bypasses api()")
	}

	runNode(t, `
var state = { dict: {} };
function esc(s) { return String(s == null ? "" : s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;"); }
function enc(s) { return encodeURIComponent(s); }
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function plural(n, one, many) { return T(n === 1 ? one : many, { n: n }); }
function ic(n) { return "<i:" + n + ">"; }
function num(n) { return String(n); }
function usd(v, d) { return "$" + Number(v || 0).toFixed(d); }
function when(x) { return "@" + x; }
function relDay(x) { return "day"; }
function dur(ms) { return Math.round(ms / 1000) + "s"; }
function skeleton() { return "<sk>"; }
function statusPill(s) { return "<pill:" + s + ">"; }
function runErr(m) { return "E:" + m; }
function mdHtml(s) { return "<md>" + esc(s) + "</md>"; }
function classifyLog(text) { return { ts: "", kind: "sys", text: text }; }
`+string(rc)+src+`
var bad = [];
function ok(c, m) { if (!c) bad.push(m); }
var blank = { id: "chat_b", title: "", origin: "chat", turns: 1, status: "idle", updated_at: "u", cost_usd: 0.5 };
var fromRun = { id: "chat_r", title: "<img src=x onerror=1>", origin: "run", origin_run_id: "run_o", turns: 2, status: "running", updated_at: "u", workspace_dir: "/home/me/code/tidebot/" };
var item = csItemHtml(fromRun, "chat_r");
ok(item.indexOf("<img") < 0 && item.indexOf("&lt;img") > 0, "list title escaped");
ok(item.indexOf('aria-current="page"') > 0 && item.indexOf("rc-dot") > 0 && item.indexOf("cs.item_tip_run") > 0, "open, busy session from a run");
ok(item.indexOf("rc.answering") > 0 && item.indexOf("cs.turns_many") < 0, "a session answering shows that, not 0 turns");
var caps = csCapsTag({ id: "c", bindings: [{ kind: "skill", id: "s1", name: "<b>pdf</b>" }, { kind: "capability", id: "c1", name: "dws" }, { kind: "skill", id: "s2", name: "gone", missing: true }] });
ok(caps.indexOf('cs.caps{&quot;n&quot;:&quot;2&quot;}') > 0 && caps.indexOf("<b>") < 0 && caps.indexOf("gone") < 0 && caps.indexOf("<button") === 0, "capabilities tag: count, escaped names, missing ones left out");
ok(csCapsTag(null) === "" && csCapsTag({ id: "c" }).indexOf("cs.caps_none_tip") > 0, "no tag on a new session; none bound");
var bi = csItemHtml(blank, "chat_r");
ok(bi.indexOf("cs.untitled") > 0 && bi.indexOf("aria-current") < 0 && bi.indexOf("rc-dot") < 0 && bi.indexOf('cs.turns_one{&quot;n&quot;:1}') > 0, "untitled blank session, one turn");
ok(csDirTag(fromRun).indexOf(">tidebot<") > 0 && csDirTag(fromRun).indexOf("/home/me/code/tidebot/") > 0, "folder: base name, full path in the tooltip");
ok(csDirTag({ workspace_dir: "C:\\Users\\me\\proj" }).indexOf(">proj<") > 0, "Windows folder");
ok(csDirTag(blank).indexOf("cs.scratch") > 0, "own empty folder");
var nw = csPageHtml([blank, fromRun], null);
ok(nw.indexOf("cs-empty") > 0 && nw.indexOf('id="rc-form"') > 0 && nw.indexOf("cs-new active") > 0 && nw.indexOf("cs-del") < 0 && nw.indexOf("cs-caps") < 0, "new session: hint, box, no delete, no capabilities yet");
ok(nw.indexOf('placeholder="cs.ph_new"') > 0, "new session placeholder");
var first = csPageHtml([], null);
ok(first.indexOf("cs-no-history") > 0 && first.indexOf("cs-blank") > 0 && first.indexOf('class="cs-side"') < 0, "first-use landing reserves no empty history column");
ok(first.indexOf('id="rc-form"') > 0 && first.indexOf("cs-empty") > 0, "first-use landing keeps welcome and composer");
ok(nw.indexOf("cs-no-history") < 0 && nw.indexOf('id="cs-list" aria-label="cs.list"') > 0 && nw.indexOf('href="#/chat/chat_r"') > 0, "new session retains accessible existing history");
var ses = { id: "chat_r", title: "t", origin: "run", origin_run_id: "run_o", turn_list: [{ index: 0, run_id: "run_o", message: "orig", status: "succeeded", answer: "a0" }, { index: 1, run_id: "run_1", message: "q1", status: "succeeded", answer: "a1" }] };
var open = csPageHtml([fromRun], ses);
ok(open.indexOf("cs-blank") < 0 && open.indexOf("cs-no-history") < 0, "active session keeps its two-column layout");
var lone = csPageHtml([], ses);
ok(lone.indexOf("cs-no-history") > 0 && lone.indexOf("cs-blank") < 0 && lone.indexOf('id="cs-del"') > 0, "active session omitted from history remains usable");
ok(open.indexOf('data-turn="0"') > 0 && open.indexOf('data-turn="1"') > 0, "the run it continues is the first turn");
ok(open.indexOf('href="#/runs/run_o"') > 0 && open.indexOf('id="cs-del"') > 0 && open.indexOf("cs-empty") < 0 && open.indexOf('id="cs-caps"') > 0, "from-run link, delete, capabilities");
ok(csHeadHtml({ id: "x", origin: "run", origin_run_id: "x\" onclick=\"y" }).indexOf("onclick") < 0, "odd run id: no link");
ok(rcBoxHtml({}).indexOf('placeholder="rc.ph"') > 0 && rcBoxHtml({}).indexOf("rc-dir") < 0, "run page box unchanged");
ok(rcOpenHtml("chat_1").indexOf('href="#/chat/chat_1"') > 0 && rcOpenHtml("").indexOf(" hidden") > 0 && rcOpenHtml("a\"b").indexOf(" hidden") > 0, "open-in-session link");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, nil)
}
