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
	for _, want := range []string{`body: over ? { expect_current: over } : {}`, `T("sd.adopt_stale"`, `e.data.code === "draft_stale") load()`, `code === "draft_stale") return T("rc.err_draft_stale")`, `class="cs-draft-stale"`} {
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
