package webui

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestChatEffortUI: the message box (run page and session page) has an effort
// select beside the permission level. It starts at the session's effort (or
// the run's before a session exists), goes with the create request, is saved
// with PUT …/effort, and is locked while a turn runs. Both languages have words.
func TestChatEffortUI(t *testing.T) {
	b, err := os.ReadFile("src/run-chat.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	app := appJSSource(t)
	for _, want := range []string{
		`rcModeHtml(o.mode || "read", o.read_tip) + rcEffortHtml(o.effort) +`,
		`effort: r._chat ? r._chat.effort : r.effort })`,
		`o.create(modeSel ? modeSel.value : undefined, effSel ? effSel.value : undefined)`,
		`body: { run_id: r.id, permission_mode: mode, effort: effort }`,
		`body: { origin: "chat", permission_mode: mode, effort: effort }`,
		`mode: s ? s.permission_mode : "exec", effort: s ? s.effort : "" })`,
		`api("/api/chat-sessions/" + enc(st.id) + "/effort", { method: "PUT", body: { effort: v } })`,
		`if (effSel) effSel.disabled = b;`,
		`document.activeElement !== effSel && !effBusy) paintEffort(s.effort);`,
		`.catch(function (e) { paintEffort(prev); showErr(rcErrText(e)); })`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if regexp.MustCompile(`\b(window\.)?(confirm|alert|prompt)\(`).MatchString(src) {
		t.Error("run-chat.js uses a native dialog")
	}
	css := cssSource(t)
	for _, want := range []string{".rc-tag.rc-effort select {", ".rc-tag.rc-effort .i { color: var(--hue-violet); }"} {
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
		for _, k := range []string{"rc.effort_auto", "rc.effort_low", "rc.effort_medium", "rc.effort_high", "rc.effort_tip", "tf.effort"} {
			if d[k] == "" {
				t.Errorf("%s: no %s", f, k)
			}
		}
		if !strings.Contains(d["rc.effort_saved"], "{e}") {
			t.Errorf("%s: rc.effort_saved = %q", f, d["rc.effort_saved"])
		}
	}

	runNode(t, `
var state = { dict: {} };
function esc(s) { return String(s == null ? "" : s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;"); }
function enc(s) { return encodeURIComponent(s); }
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function ic(n) { return "<i:" + n + ">"; }
function skeleton() { return "<sk>"; }
`+src+`
var bad = [];
function ok(c, m) { if (!c) bad.push(m); }
function sel(h) { var m = /<select id="rc-effort"[^>]*>(.*?)<\/select>/.exec(h); if (!m) return null; var s = /<option value="(\w*)" selected>/.exec(m[1]); return s ? s[1] : "?"; }
ok(sel(rcEffortHtml("high")) === "high", "high selected");
ok(sel(rcEffortHtml("")) === "", "default selected");
ok(sel(rcEffortHtml(undefined)) === "", "no value: default");
ok(sel(rcEffortHtml("max\"><b>")) === "" && rcEffortHtml("max\"><b>").indexOf("<b>") < 0, "odd value: default, not echoed");
ok((rcEffortHtml("low").match(/<option /g) || []).length === 4, "four choices");
ok(rcEffortHtml("").indexOf('aria-label="tf.effort"') > 0 && rcEffortHtml("").indexOf('data-tip="rc.effort_tip"') > 0, "label and tooltip");
var box = rcBoxHtml({ mode: "edit", effort: "medium" });
ok(box.indexOf('id="rc-mode"') > 0 && box.indexOf('id="rc-effort"') > box.indexOf('id="rc-mode"') && sel(box) === "medium", "box: effort after the level");
ok(sel(runChatHtml({ id: "a", status: "succeeded", effort: "low" })) === "low", "run page before a session: the run's effort");
ok(sel(runChatHtml({ id: "a", status: "succeeded", effort: "low", chat_session: { id: "c", turn: 0 }, _chat: { effort: "", permission_mode: "read", turn_list: [] } })) === "", "run page with a session: the session's effort");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, nil)
}
