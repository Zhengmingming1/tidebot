package webui

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	got, err := normalizeTags([]string{"  日报 ", "#Daily", "daily", "", "客户   A", "＃监控"})
	if err != nil || !reflect.DeepEqual(got, []string{"日报", "Daily", "客户 A", "监控"}) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range [][]string{{"a,b"}, {"甲，乙"}, {strings.Repeat("长", 25)}, {"a\x00"}, {"1", "2", "3", "4", "5", "6", "7", "8", "9"}} {
		if _, err := normalizeTags(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, err := normalizeTags([]string{strings.Repeat("长", 24)}); err != nil || len(got) != 1 {
		t.Fatalf("24 runes %v %v", got, err)
	}
}

func listTasks(t *testing.T, c *client) map[string][]string {
	t.Helper()
	var l []taskDTO
	if err := json.Unmarshal(c.do(http.MethodGet, "/api/tasks", "", nil).Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, d := range l {
		if d.Tags == nil {
			t.Fatalf("tags is null for %s", d.Name)
		}
		out[d.Name] = d.Tags
	}
	return out
}

// TestTaskTagsAPI: tags saved with the form (order kept, case joins the spelling in use),
// left alone when the form omits them, shown in the list, detail and form, counted for
// suggestions, copied by duplicate, carried by export/import, and added / removed in batch.
func TestTaskTagsAPI(t *testing.T) {
	c := login(t)
	code, a := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "日报", "prompt": "p", "provider": "anthropic", "tags": []string{"日报", "Daily"}})
	if code != 201 {
		t.Fatalf("create %d %v", code, a)
	}
	aid := a["id"].(string)
	_, b := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "监控", "prompt": "p", "provider": "anthropic", "tags": []string{"daily", "监控"}})
	bid := b["id"].(string)
	_, cc := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "无标签", "prompt": "p", "provider": "anthropic"})
	cid := cc["id"].(string)
	if code, e := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "坏", "prompt": "p", "tags": []string{"a,b"}}); code != 400 {
		t.Fatalf("bad tag accepted %d %v", code, e)
	}

	l := listTasks(t, c)
	if !reflect.DeepEqual(l["日报"], []string{"日报", "Daily"}) || !reflect.DeepEqual(l["监控"], []string{"Daily", "监控"}) || len(l["无标签"]) != 0 {
		t.Fatalf("list %v", l)
	}
	_, d := c.json(http.MethodGet, "/api/tasks/"+aid, nil)
	if tg, _ := d["tags"].([]any); len(tg) != 2 || tg[0] != "日报" {
		t.Fatalf("detail %v", d["tags"])
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+aid+"/form", nil)
	f := form["form"].(map[string]any)
	if tg, _ := f["tags"].([]any); len(tg) != 2 {
		t.Fatalf("form %v", f)
	}
	// an edit without "tags" (older page, agentctl) keeps them; an empty list clears them
	f2 := map[string]any{"name": "日报", "prompt": "p2", "provider": "anthropic"}
	if code, e := c.json(http.MethodPut, "/api/tasks/"+aid, f2); code != 200 {
		t.Fatalf("put %d %v", code, e)
	}
	if l := listTasks(t, c); len(l["日报"]) != 2 {
		t.Fatalf("kept %v", l)
	}

	var use []tagUse
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/tasks/tags", "", nil).Body.Bytes(), &use)
	if len(use) != 3 || use[0] != (tagUse{"Daily", 2}) {
		t.Fatalf("in use %v", use)
	}

	// batch: add to two (one already has it in another case), remove from one
	code, r := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": []string{aid, cid}, "action": "add_tag", "tag": " #监控 "})
	if code != 200 || r["done"].(float64) != 2 {
		t.Fatalf("add %d %v", code, r)
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": []string{bid}, "action": "remove_tag", "tag": "DAILY"}); code != 200 {
		t.Fatal("remove")
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/batch", map[string]any{"ids": []string{bid}, "action": "add_tag", "tag": ""}); code != 400 {
		t.Fatal("empty tag accepted")
	}
	l = listTasks(t, c)
	if !reflect.DeepEqual(l["日报"], []string{"日报", "Daily", "监控"}) || !reflect.DeepEqual(l["无标签"], []string{"监控"}) || !reflect.DeepEqual(l["监控"], []string{"监控"}) {
		t.Fatalf("after batch %v", l)
	}

	// duplicate copies; deleting hides the task's tags from suggestions
	code, dup := c.json(http.MethodPost, "/api/tasks/"+aid+"/duplicate", map[string]any{"name": "日报 副本"})
	if code != 201 {
		t.Fatalf("dup %d", code)
	}
	if l := listTasks(t, c); len(l["日报 副本"]) != 3 {
		t.Fatalf("dup tags %v", l)
	}
	c.json(http.MethodDelete, "/api/tasks/"+dup["id"].(string), nil)

	// export carries tags, import restores them
	rr := c.do(http.MethodGet, "/api/tasks/export?ids="+aid, "", nil)
	var file taskFile
	if err := json.Unmarshal(rr.Body.Bytes(), &file); err != nil || len(file.Tasks) != 1 || len(file.Tasks[0].Tags) != 3 {
		t.Fatalf("export %v %+v", err, file)
	}
	file.Tasks[0].Tags = []string{"daily", "新的"}
	code, res := c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": file, "apply": true, "pick": []int{0}})
	if code != 200 || res["created"].(float64) != 1 {
		t.Fatalf("import %d %v", code, res)
	}
	if l := listTasks(t, c); !reflect.DeepEqual(l["日报 2"], []string{"Daily", "新的"}) {
		t.Fatalf("imported %v", l)
	}
	// clear with an empty list
	f2["tags"] = []string{}
	c.json(http.MethodPut, "/api/tasks/"+aid, f2)
	if l := listTasks(t, c); len(l["日报"]) != 0 {
		t.Fatalf("cleared %v", l)
	}
}

// TestTaskTagsUI: list filter / chips / batch, the form's chip input and the detail line
// are wired, strings exist in both languages, no native dialogs, names are escaped and
// hues are stable.
func TestTaskTagsUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`(!v.tag || (t.tags || []).indexOf(v.tag) >= 0)`,
		`(t.tags || []).join(" ")).toLowerCase().indexOf(q) >= 0`,
		`tagChipsRow(t.tags)`,
		`if (taskView.tag) q.push("tag=" + enc(taskView.tag));`,
		`var tgs = tagInput($("#tg-host", root), tf.tags || pre.tags || [], inUse);`,
		`policy: readPolicyFields(f), tags: tgs.value() };`,
		`api("/api/tasks/batch", { method: "POST", body: { ids: ids, action: act, tag: t } })`,
		`href="#/tasks?tag=' + enc(g) + '"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	block := cut(t, src, "  // ---------- task tags ----------", "  // ---------- TASKS ----------")
	for _, bad := range []string{"confirm(", "alert(", "prompt("} {
		if strings.Contains(block, bad) {
			t.Errorf("tags block uses %s", bad)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(f, err)
		}
		for _, k := range []string{"tg.label", "tg.hint", "tg.ph", "tg.any", "tg.filter_tip", "tg.batch", "tg.batch_title", "tg.added", "tg.removed", "tg.in_use", "tg.n_tasks", "tg.remove"} {
			if strings.TrimSpace(m[k]) == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, w := range []string{".ttag.h5 {", ".tag-in:focus-within", ".tl-line {"} {
		if !strings.Contains(string(css), w) {
			t.Errorf("styles.css lacks %s", w)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `
var bad = [];
function T(k, v) { return k + (v ? JSON.stringify(v) : ""); }
function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;"); }
function ic(n) { return "<i " + n + ">"; }
` + cut(t, block, "  var TAG_MAX", "  // tagInput:") + `
if (tagHue("Daily") !== tagHue("daily")) bad.push("hue depends on case");
var seen = {}; ["日报","监控","客户 A","report","ops","news","x","y"].forEach(function (g) { var h = tagHue(g); if (h < 0 || h > 5) bad.push("hue " + h); seen[h] = 1; });
if (Object.keys(seen).length < 3) bad.push("hues not spread");
if (cleanTag("  #日报,  早  ") !== "日报 早") bad.push("clean: " + cleanTag("  #日报,  早  "));
if (cleanTag("x".repeat(40)).length !== 24) bad.push("clean len");
var row = tagChipsRow(["<b>", "b", "c", "d"]);
if (/<b>/.test(row)) bad.push("not escaped: " + row);
if ((row.match(/data-tagf=/g) || []).length !== 2 || row.indexOf("+2") < 0) bad.push("row: " + row);
if (tagChipsRow([]) !== "" || tagChipsRow(null) !== "") bad.push("empty row");
console.log(JSON.stringify(bad));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var fails []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &fails); err != nil {
		t.Fatalf("node output %s", out)
	}
	for _, f := range fails {
		t.Error(f)
	}
}
