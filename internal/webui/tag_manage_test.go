package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

func tagsOfTask(t *testing.T, c *client, id string) []string {
	t.Helper()
	m, err := c.s.taskTags(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if m[id] == nil {
		return []string{}
	}
	return m[id]
}

// TestTagRenameMergeDelete: Settings › Tags renames a tag on every task (archived ones too,
// counted apart), merges onto a name in use (its spelling, one copy per task, earlier
// position), deletes it, and each answer's "before" restores the tasks exactly.
func TestTagRenameMergeDelete(t *testing.T) {
	c := login(t)
	mk := func(name string, tags ...string) string {
		code, r := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": name, "prompt": "p", "provider": "anthropic", "tags": tags})
		if code != 201 {
			t.Fatalf("create %s %d %v", name, code, r)
		}
		return r["id"].(string)
	}
	a := mk("A", "日报", "Daily")
	b := mk("B", "daily", "监控") // joins "Daily"
	cc := mk("C", "监控")
	d := mk("D", "Daily", "旧")
	c.json(http.MethodDelete, "/api/tasks/"+d, nil) // archived: keeps its tags, not counted

	// rename (case-insensitive match of the old name)
	code, r := c.json(http.MethodPost, "/api/tasks/tags/rename", map[string]any{"from": "DAILY", "to": " #每日 "})
	if code != 200 || r["tag"] != "每日" || r["tasks"].(float64) != 2 || r["merged"] != false || len(r["before"].([]any)) != 3 {
		t.Fatalf("rename %d %v", code, r)
	}
	if g := tagsOfTask(t, c, a); !reflect.DeepEqual(g, []string{"日报", "每日"}) {
		t.Fatalf("A %v", g)
	}
	if g := tagsOfTask(t, c, d); !reflect.DeepEqual(g, []string{"每日", "旧"}) {
		t.Fatalf("archived D %v", g)
	}
	before1 := r["before"]

	// merge onto a tag in use: B has both and keeps one, in the earlier slot
	code, r = c.json(http.MethodPost, "/api/tasks/tags/rename", map[string]any{"from": "每日", "to": "监控"})
	if code != 200 || r["merged"] != true || r["tag"] != "监控" || r["tasks"].(float64) != 2 {
		t.Fatalf("merge %d %v", code, r)
	}
	if g := tagsOfTask(t, c, b); !reflect.DeepEqual(g, []string{"监控"}) {
		t.Fatalf("B merged %v", g)
	}
	if g := tagsOfTask(t, c, a); !reflect.DeepEqual(g, []string{"日报", "监控"}) {
		t.Fatalf("A merged %v", g)
	}
	var use []tagUse
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/tasks/tags", "", nil).Body.Bytes(), &use)
	if len(use) != 2 || use[0] != (tagUse{"监控", 3}) {
		t.Fatalf("in use after merge %v", use)
	}
	// undo the merge, then the rename: exactly as created
	if code, r := c.json(http.MethodPost, "/api/tasks/tags/restore", map[string]any{"before": r["before"]}); code != 200 || r["tasks"].(float64) != 3 {
		t.Fatalf("restore merge %d %v", code, r)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/tags/restore", map[string]any{"before": before1}); code != 200 {
		t.Fatal("restore rename")
	}
	for id, want := range map[string][]string{a: {"日报", "Daily"}, b: {"Daily", "监控"}, cc: {"监控"}, d: {"Daily", "旧"}} {
		if g := tagsOfTask(t, c, id); !reflect.DeepEqual(g, want) {
			t.Fatalf("restored %s %v want %v", id, g, want)
		}
	}

	// a change of case only is a rename, not a merge
	if code, r := c.json(http.MethodPost, "/api/tasks/tags/rename", map[string]any{"from": "Daily", "to": "daily"}); code != 200 || r["merged"] != false || r["tag"] != "daily" {
		t.Fatalf("case %d %v", code, r)
	}

	// delete takes it off every task; the tasks stay
	code, r = c.json(http.MethodPost, "/api/tasks/tags/delete", map[string]any{"tag": "监控"})
	if code != 200 || r["tasks"].(float64) != 2 {
		t.Fatalf("delete %d %v", code, r)
	}
	if g := tagsOfTask(t, c, cc); len(g) != 0 {
		t.Fatalf("C after delete %v", g)
	}
	if l := listTasks(t, c); len(l) != 3 {
		t.Fatalf("tasks after delete %v", l)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/tags/restore", map[string]any{"before": r["before"]}); code != 200 {
		t.Fatal("restore delete")
	}
	if g := tagsOfTask(t, c, cc); !reflect.DeepEqual(g, []string{"监控"}) {
		t.Fatalf("C restored %v", g)
	}

	// refusals
	for _, x := range []struct {
		path string
		body map[string]any
		want int
	}{
		{"rename", map[string]any{"from": "nope", "to": "x"}, 404},
		{"rename", map[string]any{"from": "旧", "to": "x"}, 404}, // only on an archived task
		{"rename", map[string]any{"from": "日报", "to": "日报"}, 400},
		{"rename", map[string]any{"from": "日报", "to": "a,b"}, 400},
		{"rename", map[string]any{"from": "日报", "to": strings.Repeat("长", 25)}, 400},
		{"rename", map[string]any{"from": "", "to": "x"}, 400},
		{"delete", map[string]any{"tag": "x' OR 1=1 --"}, 404},
		{"restore", map[string]any{}, 400},
		{"restore", map[string]any{"before": []any{map[string]any{"id": a, "tags": []string{"a,b"}}}}, 400},
		{"nope", map[string]any{}, 404},
	} {
		if code, r := c.json(http.MethodPost, "/api/tasks/tags/"+x.path, x.body); code != x.want {
			t.Fatalf("%s %v: %d %v (want %d)", x.path, x.body, code, r, x.want)
		}
	}
	// unknown tasks in a restore are skipped
	if code, r := c.json(http.MethodPost, "/api/tasks/tags/restore", map[string]any{"before": []any{map[string]any{"id": "missing", "tags": []string{"x"}}}}); code != 200 || r["tasks"].(float64) != 0 {
		t.Fatalf("restore unknown %d %v", code, r)
	}
	if g := tagsOfTask(t, c, a); !reflect.DeepEqual(g, []string{"日报", "daily"}) {
		t.Fatalf("refusals changed A: %v", g)
	}
	// GET is not an edit; a request without the CSRF token changes nothing
	if code, _ := c.json(http.MethodGet, "/api/tasks/tags/delete", nil); code == 200 {
		t.Fatal("GET deleted")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/tags/delete", bytes.NewReader([]byte(`{"tag":"日报"}`)))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
	if g := tagsOfTask(t, c, a); g[0] != "日报" {
		t.Fatalf("csrf-less delete changed A: %v", g)
	}
}

// TestTagManagerUI: the section exists in the settings nav (and so in the palette), each row
// links to the filtered list, delete goes through the in-app confirm, changes carry an undo,
// the task list links to it, and both languages have every string.
func TestTagManagerUI(t *testing.T) {
	src := appJS(t)
	for _, want := range []string{
		`"pricing", "tags", "security"`, `tags: "tag"`,
		`section === "tags"`, `tagManager($("#tgm-box"))`,
		`href="#/tasks?tag=' + enc(u.tag) + '"`,
		`confirmDialog({ title: T("tgm.del_title"`,
		`"/api/tasks/tags/restore"`, `undoOf(r.before)`,
		`id="tgm-link" href="#/settings/tags"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	i := strings.Index(src, "function tagManager(")
	j := strings.Index(src, "// ---------- SETTINGS ----------")
	if i < 0 || j < i {
		t.Fatal("tagManager not found")
	}
	sec := src[strings.Index(src, "function tagNameCheck("):j]
	for _, bad := range []string{"window.confirm", "alert(", "prompt("} {
		if strings.Contains(sec, bad) {
			t.Errorf("native dialog %q in the tag manager", bad)
		}
	}
	zh, en := map[string]string{}, map[string]string{}
	for p, m := range map[string]map[string]string{"static/i18n/zh-CN.json": zh, "static/i18n/en.json": en} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"setnav.tags", "tgm.sub", "tgm.none", "tgm.tasks_one", "tgm.tasks_many", "tgm.rename_title", "tgm.merge_hint", "tgm.bad", "tgm.renamed", "tgm.merged", "tgm.del_title", "tgm.del_body", "tgm.deleted", "tgm.restored", "tgm.manage"} {
		if zh[k] == "" || en[k] == "" {
			t.Errorf("missing %s", k)
		}
	}

	// tagNameCheck under node: unchanged / bad / merge (with the existing spelling) / plain rename
	runNode(t, `
var TAG_LEN = 24;
eval(SECTION);
var inUse = [{ tag: "Daily", n: 3 }, { tag: "监控", n: 2 }, { tag: "ops", n: 1 }];
var bad = [], eq = function (got, k, tt) { if (got.k !== k || (tt !== undefined && got.t !== tt)) bad.push(JSON.stringify(got) + " want " + k + " " + tt); };
eq(tagNameCheck("ops", "ops", inUse), "same");
eq(tagNameCheck("ops", "  ", inUse), "same");
eq(tagNameCheck("ops", "a,b", inUse), "bad");
eq(tagNameCheck("ops", "甲，乙", inUse), "bad");
eq(tagNameCheck("ops", "长".repeat(25), inUse), "bad");
eq(tagNameCheck("ops", "长".repeat(24), inUse), "");
eq(tagNameCheck("ops", "daily", inUse), "merge", "Daily");
eq(tagNameCheck("ops", "#监控", inUse), "merge", "监控");
eq(tagNameCheck("ops", "OPS", inUse), "", "OPS");
eq(tagNameCheck("ops", " new   name ", inUse), "", "new name");
if (tagNameCheck("ops", "daily", inUse).n !== 3) bad.push("merge count");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, map[string]string{"SECTION": src[strings.Index(src, "function tagNameCheck("):i]})
}
