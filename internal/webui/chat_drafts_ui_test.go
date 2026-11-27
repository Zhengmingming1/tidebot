package webui

import (
	"strings"
	"testing"
)

// The draft bar adopts only after the user confirms, and the task form only
// suggests a schedule read from the prompt instead of filling it in.
func TestChatDraftUIMarkup(t *testing.T) {
	b := []byte(appJSSource(t))
	js := string(b)
	for _, want := range []string{`function csDraftBar(`, `id="cs-tune"`, `id="cs-save-task"`, `"/task-preset"`, `"/task-preset?from=run"`, `api(base + "/adopt"`, `suggest(g.cron);`, `id="af-sched"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(js, `se.set(g.cron)`) {
		t.Fatal("prompt schedule is still filled in automatically")
	}
	i := strings.Index(js, `api(base + "/adopt"`)
	if j := strings.LastIndex(js[:i], "confirmDialog("); j < 0 || i-j > 800 {
		t.Fatal("adopt is not behind a confirm dialog")
	}
}

// 采用 names the newer current version it would replace (and sends it back, so the
// server replaces only that one); a draft_stale answer refreshes the bar.
func TestChatDraftStaleAdoptUI(t *testing.T) {
	js := appJS(t)
	i := strings.Index(js, "function draftStale(")
	if i < 0 {
		t.Fatal("no draftStale")
	}
	j := strings.Index(js[i:], "\n  }\n")
	fn := js[i : i+j+4]
	for _, want := range []string{`body: over ? { skill_id: sid, expect_current: over } : { skill_id: sid }`, `T("sd.adopt_stale"`, `e.data.code === "draft_stale") load()`, `code === "draft_stale") return T("rc.err_draft_stale")`, `class="cs-draft-stale"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		b, err := Static.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"sd.stale_tip"`, `"sd.adopt_stale"`, `"rc.err_draft_stale"`} {
			if !strings.Contains(string(b), k) {
				t.Fatalf("%s: missing %s", lang, k)
			}
		}
	}
	runNode(t, `eval(FN);
var bad = [];
function ok(name, d, want) { if (draftStale(d) !== want) bad.push(name); }
ok("same", { current_version_id: "v1", base_version_id: "v1" }, false);
ok("adopted here", { current_version_id: "v2", base_version_id: "v1", saved: { version_id: "v2" } }, false);
ok("changed elsewhere", { current_version_id: "v3", base_version_id: "v1", saved: { version_id: "v2" } }, true);
ok("nothing saved yet", { current_version_id: "v3", base_version_id: "v1" }, true);
ok("old server", { base_version_id: "v1" }, false);
ok("no draft", null, false);
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, map[string]string{"FN": fn})
}

// fnSrc cuts a two-space-indented function of the chat page out of the source.
func fnSrc(t *testing.T, js, name string) string {
	t.Helper()
	i := strings.Index(js, "  function "+name+"(")
	if i < 0 {
		t.Fatalf("no %s", name)
	}
	j := strings.Index(js[i:], "\n  }\n")
	return js[i : i+j+4]
}

// Several drafts in one session: one row each (names escaped), save / adopt /
// discard say which skill, 用草稿试跑 once on the first row, and 改技能 stays
// while a skill of the session has no draft yet (and offers only those).
func TestChatDraftsSeveralUI(t *testing.T) {
	js := appJS(t)
	for _, want := range []string{`body: { skill_id: sid }`, `base + "?skill_id=" + enc(sid), { method: "DELETE" }`, `tune.hidden = !!(list.length && !csDraftFree(d).length)`, `csPickSkill(sk)`, `class="cs-drafts" id="cs-draft"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s", want)
		}
	}
	css, err := Static.ReadFile("static/styles.css")
	if err != nil || !strings.Contains(string(css), ".cs-drafts{") && !strings.Contains(string(css), ".cs-drafts {") {
		t.Fatalf("no .cs-drafts style %v", err)
	}
	for _, lang := range []string{"zh-CN", "en"} {
		b, err := Static.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"sd.all_drafted"`, `"sd.discard_named"`, `"sd.discard_body_named"`} {
			if !strings.Contains(string(b), k) {
				t.Fatalf("%s: missing %s", lang, k)
			}
		}
	}
	src := fnSrc(t, js, "csDraftList") + fnSrc(t, js, "csDraftFree") + fnSrc(t, js, "csDraftRowHtml") + fnSrc(t, js, "draftStale")
	runNode(t, `function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }
function ic() { return "<i></i>"; }
eval(FN);
var bad = [];
function ok(name, c) { if (!c) bad.push(name); }
var d = { active: true, skill_id: "s1", drafts: [
  { active: true, skill_id: "s1", skill_name: "<b>one</b>", base_no: 1, unsaved: true },
  { active: true, skill_id: "s2", skill_name: "two", base_no: 3, saved: { no: 4, status: "pending" } }],
  skills: [{ id: "s1" }, { id: "s2" }, { id: "s3" }] };
ok("list", csDraftList(d).length === 2);
ok("old server", csDraftList({ active: true, skill_id: "s1" }).length === 1 && csDraftList({ active: false }).length === 0 && csDraftList(null).length === 0);
ok("free", JSON.stringify(csDraftFree(d).map(function (k) { return k.id; })) === '["s3"]');
ok("none free", csDraftFree({ drafts: d.drafts, skills: [{ id: "s1" }, { id: "s2" }] }).length === 0);
var r1 = csDraftRowHtml(d.drafts[0], true, true, ""), r2 = csDraftRowHtml(d.drafts[1], false, true, "");
ok("row skill", r1.indexOf('data-skill="s1"') > 0 && r2.indexOf('data-skill="s2"') > 0);
ok("escaped", r1.indexOf("<b>one</b>") < 0 && r1.indexOf("&lt;b&gt;one") > 0);
ok("try once", r1.indexOf('data-dr="try"') > 0 && r2.indexOf('data-dr="try"') < 0);
ok("save only unsaved", /data-dr="save"(?! disabled)/.test(r1) && /data-dr="save" disabled/.test(r2));
ok("adopt only pending", /data-dr="adopt" disabled/.test(r1) && /data-dr="adopt"(?! disabled)/.test(r2));
ok("busy", csDraftRowHtml(d.drafts[1], true, true, " disabled").indexOf('data-dr="adopt" disabled') > 0);
ok("read-only eye", csDraftRowHtml(d.drafts[0], true, false, "").indexOf("cs-draft-ro") > 0);
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, map[string]string{"FN": src})
}
