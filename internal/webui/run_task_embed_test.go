package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// TestRunDetailEmbedsTask: GET /api/runs/{id}?task=1 (the run page on a window wide enough
// for the inspector) brings the run's task along, the same as the three answers the
// inspector otherwise asks for once the page is drawn; without task=1, for a run without a
// task or whose task is gone, nothing is added.
func TestRunDetailEmbedsTask(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	tv, err := c.s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: "inspected", Prompt: "work", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	tid := tv.Task.ID
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.s.deps.DB.Exec(`INSERT INTO runs(id,task_id,status,prompt,created_at) VALUES('run_k',?,'succeeded','p',?)`, tid, now); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.deps.DB.Exec(`INSERT INTO runs(id,status,prompt,created_at) VALUES('run_q','succeeded','p',?)`, now); err != nil {
		t.Fatal(err)
	}
	get := func(path string) map[string]any {
		t.Helper()
		code, out := c.json(http.MethodGet, path, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", path, code, out)
		}
		return out
	}
	raw := func(path string) string {
		t.Helper()
		rr := c.do(http.MethodGet, path, "", nil)
		if rr.Code != 200 {
			t.Fatalf("%s: %d", path, rr.Code)
		}
		return rr.Body.String()
	}
	same := func(what string, got any, want string) {
		t.Helper()
		var w any
		if err := json.Unmarshal([]byte(want), &w); err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(w)
		if string(a) != string(b) {
			t.Fatalf("%s differs:\n%s\n%s", what, a, b)
		}
	}
	check := func(label string) {
		t.Helper()
		ct, _ := get("/api/runs/run_k?chat=1&task=1")["current_task"].(map[string]any)
		if ct == nil {
			t.Fatalf("%s: no current_task", label)
		}
		same(label+" task", ct["task"], raw("/api/tasks/"+tid))
		// the page reads an empty list (null) as [] and a failed read as null; the embed says which
		am := strings.TrimSpace(raw("/api/tasks/" + tid + "/amendments"))
		if am == "null" {
			am = "[]"
		}
		same(label+" amendments", ct["amendments"], am)
		var f map[string]any
		_ = json.Unmarshal([]byte(raw("/api/tasks/"+tid+"/form")), &f)
		if f["form"] == nil {
			t.Fatalf("form endpoint: %v", f)
		}
		a, _ := json.Marshal(ct["form"])
		b, _ := json.Marshal(f["form"])
		if string(a) != string(b) {
			t.Fatalf("%s form differs:\n%s\n%s", label, a, b)
		}
	}
	check("no amendments")
	if ct, _ := get("/api/runs/run_k?task=1")["current_task"].(map[string]any); ct == nil {
		t.Fatal("task=1 alone should embed")
	} else if l, ok := ct["amendments"].([]any); !ok || len(l) != 0 {
		t.Fatalf("empty amendments should be [], got %v", ct["amendments"])
	}
	if code, _ := c.json(http.MethodPost, "/api/tasks/"+tid+"/amendments", map[string]any{"utterance": "always add a summary"}); code != 200 {
		t.Fatalf("propose: %d", code)
	}
	check("with amendments")
	for _, p := range []string{"/api/runs/run_k", "/api/runs/run_k?chat=1", "/api/runs/run_k?chat=1&task=0", "/api/runs/run_q?chat=1&task=1"} {
		if v, ok := get(p)["current_task"]; ok {
			t.Errorf("%s carries current_task %v", p, v)
		}
	}
	// the task is gone (a run kept for the audit trail): the page asks as before and shows the error
	if _, err := c.s.deps.DB.Exec(`PRAGMA foreign_keys=OFF; UPDATE runs SET task_id='task_gone' WHERE id='run_k'`); err != nil {
		t.Fatal(err)
	}
	if v, ok := get("/api/runs/run_k?chat=1&task=1")["current_task"]; ok {
		t.Errorf("missing task embedded: %v", v)
	}
}

// TestRunInspectorTaskEmbedUI: the run page asks for task=1 only where the inspector opens
// with the page, and the inspector draws an embedded task without the three requests.
func TestRunInspectorTaskEmbedUI(t *testing.T) {
	b, err := os.ReadFile("src/log-view.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`var media = window.matchMedia("(min-width: 1101px)");`,
		`window.matchMedia("(min-width: 1101px)").matches; // = bindRunWorkspace, styles.css`,
		`if (ct && ct.task && ct.task.id === r.task_id) { r.current_task = null; host.innerHTML = runTaskInspectorHtml(ct.task, ct.amendments, ct.form); return; }`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("log-view.js lost %q", want)
		}
	}
	css, err := os.ReadFile("src/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "@media (min-width: 1101px)") {
		t.Error("styles.css: inspector breakpoint moved; keep log-view.js in step")
	}
	path := cut(t, src, "  function runDetailPath(id) {", "  function runBindingsPath(id) {")
	runNode(t, `
var bad = [];
function enc(s) { return encodeURIComponent(s); }
`+path+`
if (runDetailPath("r 1") !== "/api/runs/r%201?chat=1") bad.push("no window: " + runDetailPath("r 1"));
var asked = "";
var window = { matchMedia: function (q) { asked = q; return { matches: true }; } };
if (runDetailPath("r 1") !== "/api/runs/r%201?chat=1&task=1" || asked !== "(min-width: 1101px)") bad.push("wide: " + runDetailPath("r 1") + " " + asked);
window.matchMedia = function () { return { matches: false }; };
if (runDetailPath("r 1") !== "/api/runs/r%201?chat=1") bad.push("narrow: " + runDetailPath("r 1"));
if (bad.length) { console.error(bad.join("\n")); process.exit(1); }
`, nil)
}
