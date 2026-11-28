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

// Several draft rows line up: each row is one label cell (stale mark inside it)
// plus one direct child per button, and with CSS subgrid every row places
// 试跑 / 保存 / 采用 / ✕ in the same column, so a longer 采用 vN or a row
// without 试跑 no longer shifts the buttons (measured at 1024×655: 16 px → 0).
func TestChatDraftRowsAligned(t *testing.T) {
	js := appJS(t)
	src := fnSrc(t, js, "csDraftRowHtml") + fnSrc(t, js, "draftStale")
	runNode(t, `function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }
function ic() { return '<svg class="i"></svg>'; }
eval(FN);
var bad = [];
function ok(name, c) { if (!c) bad.push(name); }
// the direct children of the row's outer div, as "tag.class" / "button[data-dr]"
function kids(html) {
  var re = /<(\/?)(div|span|button|strong|svg)\b([^>]*)>/g, m, depth = 0, out = [];
  while ((m = re.exec(html))) {
    if (m[1]) { depth--; continue; }
    if (depth === 1) {
      var dr = /data-dr="(\w+)"/.exec(m[3]), cl = /class="([^"]*)"/.exec(m[3]);
      out.push(dr ? dr[1] : (cl ? cl[1] : m[2]));
    }
    depth++;
  }
  ok("balanced " + html.slice(0, 40), depth === 0);
  return out.join(",");
}
var stale = { skill_id: "s1", skill_name: "one", base_no: 1, current_no: 3, base_version_id: "v1", current_version_id: "v3" };
var pend = { skill_id: "s2", skill_name: "two", base_no: 3, saved: { no: 4, status: "pending" } };
var r1 = csDraftRowHtml(stale, true, true, ""), r2 = csDraftRowHtml(pend, false, false, "");
ok("first row cells: " + kids(r1), kids(r1) === "cs-draft-main,try,save,adopt,discard");
ok("other row cells: " + kids(r2), kids(r2) === "cs-draft-main,save,adopt,discard");
ok("stale mark inside the label cell", r1.indexOf("cs-draft-stale") > r1.indexOf("cs-draft-main") && r1.indexOf("cs-draft-stale") < r1.indexOf('data-dr="try"'));
ok("has-try only on the row with 试跑", /^<div class="cs-draft has-try"/.test(r1) && /^<div class="cs-draft"/.test(r2));
ok("no flex spacer left", r1.indexOf("rc-sp") < 0);
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, map[string]string{"FN": src})

	b, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	// the bundled stylesheet, with the minifier's line breaks (CRLF in a Windows
	// checkout) and last semicolons dropped
	css := strings.NewReplacer("\r", "", "\n", "", ";}", "}", "@supports (", "@supports(").Replace(string(b))
	i := strings.Index(css, "@supports(grid-template-columns:subgrid)")
	if i < 0 {
		t.Fatal("no subgrid block for the draft rows")
	}
	block := css[i:]
	end := strings.Index(block, "}}")
	if end < 0 {
		t.Fatalf("subgrid block not closed: %.200s", block)
	}
	block = block[:end+2]
	for _, want := range []string{
		`.cs-drafts{display:grid;grid-template-columns:minmax(0,1fr) repeat(4,auto)`,
		`.cs-draft{grid-column:1/-1;display:grid;grid-template-columns:subgrid`,
		`.cs-draft>.cs-draft-main{grid-column:1/3`,
		`.cs-draft.has-try>.cs-draft-main{grid-column:1}`,
		`.cs-draft>[data-dr="try"]{grid-column:2}`,
		`.cs-draft>[data-dr="save"]{grid-column:3}`,
		`.cs-draft>[data-dr="adopt"]{grid-column:4}`,
		`.cs-draft>[data-dr="discard"]{grid-column:5}`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("subgrid block lacks %s:\n%s", want, block)
		}
	}
	// hidden still wins over the grid display (attribute selector is more specific)
	for _, want := range []string{`.cs-drafts[hidden]{display:none}`, `.cs-draft[hidden]{display:none}`} {
		if !strings.Contains(css, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
