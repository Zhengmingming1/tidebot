package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// TestUndoChangesAPI: POST /api/runs/{id}/undo-changes passes the run and direction on,
// answers refusals as 409 with their code and git's detail, 404 for unknown runs, and
// refuses GET and requests without the CSRF token; the detail and the list show the state.
func TestUndoChangesAPI(t *testing.T) {
	c := login(t)
	seedRun(t, c, "ur1", "p", "succeeded", 0, time.Now())
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET run_mode='edit', workspace_dir='/w', file_changes='{"files":[],"count":2,"add":3,"del":1}' WHERE id='ur1'`); err != nil {
		t.Fatal(err)
	}
	var gotID string
	var gotRedo bool
	var next error
	c.s.deps.UndoRunChanges = func(ctx context.Context, id string, redo bool) (string, error) {
		gotID, gotRedo = id, redo
		if next != nil {
			return "", next
		}
		at := ""
		if !redo {
			at = "2026-10-31T12:00:00Z"
		}
		_, err := c.s.deps.DB.Exec(`UPDATE runs SET changes_undone_at=? WHERE id=?`, at, id)
		return at, err
	}
	code, out := c.json(http.MethodPost, "/api/runs/ur1/undo-changes", map[string]any{})
	if code != 200 || out["changes_undone_at"] != "2026-10-31T12:00:00Z" || gotID != "ur1" || gotRedo {
		t.Fatalf("undo %d %v %s %v", code, out, gotID, gotRedo)
	}
	if code, out = c.json(http.MethodGet, "/api/runs/ur1", nil); code != 200 || out["changes_undone_at"] != "2026-10-31T12:00:00Z" {
		t.Fatalf("detail %v", out)
	}
	rec := c.do(http.MethodGet, "/api/runs?limit=5", "", nil)
	if !strings.Contains(rec.Body.String(), `"undone":true`) {
		t.Fatalf("list lacks undone: %s", rec.Body.String())
	}
	if code, out = c.json(http.MethodPost, "/api/runs/ur1/undo-changes", map[string]any{"redo": true}); code != 200 || out["changes_undone_at"] != "" || !gotRedo {
		t.Fatalf("redo %d %v", code, out)
	}
	if rec := c.do(http.MethodGet, "/api/runs?limit=5", "", nil); strings.Contains(rec.Body.String(), `"undone"`) {
		t.Fatal("list still says undone")
	}
	next = &UndoError{Code: "conflict", Detail: "error: a.txt: patch does not apply"}
	if code, out = c.json(http.MethodPost, "/api/runs/ur1/undo-changes", map[string]any{}); code != 409 || out["code"] != "conflict" || out["detail"] != "error: a.txt: patch does not apply" {
		t.Fatalf("conflict %d %v", code, out)
	}
	next = ErrRunNotFound
	if code, _ = c.json(http.MethodPost, "/api/runs/nope/undo-changes", map[string]any{}); code != 404 {
		t.Fatalf("unknown %d", code)
	}
	next = errors.New("boom")
	if code, _ = c.json(http.MethodPost, "/api/runs/ur1/undo-changes", map[string]any{}); code != 500 {
		t.Fatalf("error %d", code)
	}
	next = nil
	if rec := c.do(http.MethodPost, "/api/runs/ur1/undo-changes", "application/json", []byte(`{"redo":`)); rec.Code != 400 {
		t.Fatalf("bad body %d", rec.Code)
	}
	if code, _ = c.json(http.MethodGet, "/api/runs/ur1/undo-changes", nil); code != 404 {
		t.Fatalf("GET %d", code)
	}
	gotID = ""
	req := httptest.NewRequest(http.MethodPost, "/api/runs/ur1/undo-changes", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 || gotID != "" {
		t.Fatalf("no CSRF: %d, called %q", rr.Code, gotID)
	}
	c.s.deps.UndoRunChanges = nil
	if code, _ = c.json(http.MethodPost, "/api/runs/ur1/undo-changes", map[string]any{}); code != 503 {
		t.Fatalf("no handler %d", code)
	}
}

// TestUndoChangesUI: the card offers Undo / Put back (not while running, not for a cut
// patch), marks an undone run, uses the in-app confirm, names conflicting files from
// git's message; the list badge reads as undone; both languages carry every text.
func TestUndoChangesUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`api("/api/runs/" + enc(r.id) + "/undo-changes", { method: "POST", body: { redo: redo } })`,
		`return confirmDialog({ title: T("rd.undo_title"`,
		`toast(T("rd.chg_undone_t", { n: num(n) }), false, function () { runUndoChanges(r, true); }, T("rd.chg_redo"));`,
		`if (ub) ub.addEventListener("click", function () { runUndoChanges(r, ub.getAttribute("data-chg-undo") === "redo", ub); });`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	for _, bad := range []string{"window.confirm(", "alert(", "prompt("} {
		if i := strings.Index(src, "function runUndoChanges"); strings.Contains(src[i:i+1500], bad) {
			t.Errorf("runUndoChanges uses %s", bad)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".rd-chg.undone .rd-chg-list", ".chg-badge.undone"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles lack %s", s)
		}
	}
	keys := []string{"rd.chg_undo", "rd.chg_undo_tip", "rd.chg_redo", "rd.chg_redo_tip", "rd.chg_undone", "rd.chg_undone_tip", "rd.chg_undone_t", "rd.chg_redone",
		"rd.undo_title", "rd.undo_body", "rd.undo_off_live", "rd.undo_off_cut", "rd.undo_err_conflict", "rd.undo_err_conflict_f", "runs.chg_undone_tip"}
	for _, code := range []string{"none", "running", "cut", "binary", "state", "dir", "busy"} {
		keys = append(keys, "rd.undo_err_"+code)
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
		for k, ph := range map[string]string{"rd.undo_title": "{n}", "rd.undo_body": "{dir}", "rd.chg_undone_tip": "{t}", "rd.undo_err_conflict_f": "{f}", "rd.chg_undone_t": "{n}"} {
			if !strings.Contains(m[k], ph) {
				t.Errorf("%s %s lacks %s", f, k, ph)
			}
		}
	}
	fn := regexp.MustCompile(`(?s)  function runChangesHtml\(r\) \{\n.*?\n  function bindRunChanges`).FindString(src)
	badge := regexp.MustCompile(`(?s)  function runChgBadge\(r\) \{\n.*?\n  \}\n`).FindString(src)
	if fn == "" || badge == "" {
		t.Fatal("functions not found")
	}
	fn = strings.TrimSuffix(fn, "  function bindRunChanges")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");}
var state = { dict: { "rd.undo_err_busy": "x" } };
function T(k,v){return k+(v?JSON.stringify(v):"");} function ic(n){return "<i>"+n+"</i>";} function num(n){return String(n);}
function when(t){return "when:"+t;} function enc(s){return encodeURIComponent(s);} function plural(n,a,b){return T(n===1?a:b,{n:n});}
` + fn + badge + `
var fail = function (m, h) { console.error(m + "\n" + h); process.exitCode = 1; };
var c = { files: [{ path: "a", status: "M", add: 1, del: 1 }], count: 1, add: 1, del: 1 };
var h = runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: c });
if (h.indexOf('data-chg-undo="undo"') < 0 || / data-chg-undo="undo" disabled/.test(h) || h.indexOf("rd.chg_undo_tip") < 0 || h.indexOf(" undone") >= 0) fail("undo button", h);
h = runChangesHtml({ id: "r1", run_mode: "edit", status: "running", file_changes: c });
if (h.indexOf('data-chg-undo="undo" disabled') < 0 || h.indexOf("rd.undo_off_live") < 0) fail("running", h);
h = runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: { files: c.files, count: 1, patch_cut: true } });
if (h.indexOf('data-chg-undo="undo" disabled') < 0 || h.indexOf("rd.undo_off_cut") < 0) fail("cut", h);
h = runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", changes_undone_at: "2026-10-31T12:00:00Z", file_changes: c });
if (h.indexOf('data-chg-undo="redo"') < 0 || h.indexOf('class="card rd-chg undone"') < 0 || h.indexOf("rd.chg_undone<") < 0 || h.indexOf("when:2026-10-31T12:00:00Z") < 0) fail("undone", h);
if (runChangesHtml({ id: "r1", run_mode: "edit", status: "succeeded", file_changes: { files: [], count: 0 } }).indexOf("data-chg-undo") >= 0) fail("nothing to undo", "");
var f = undoConflictFiles("error: patch failed: src/<b>.go:12\nerror: src/<b>.go: patch does not apply\nerror: 新建/笔记.md: already exists in working directory\nerror: while searching for:");
if (JSON.stringify(f) !== JSON.stringify(["src/<b>.go", "新建/笔记.md"])) fail("conflict files", JSON.stringify(f));
var e1 = undoErrText({ data: { code: "conflict", detail: "error: a.txt: patch does not apply" } });
if (e1.indexOf("rd.undo_err_conflict_f") !== 0 || e1.indexOf("a.txt") < 0) fail("conflict text", e1);
if (undoErrText({ data: { code: "conflict", detail: "" } }) !== "rd.undo_err_conflict") fail("conflict plain", "");
if (undoErrText({ data: { code: "busy" } }) !== "rd.undo_err_busy" || undoErrText({ message: "boom", data: {} }) !== "boom") fail("codes", "");
var b = runChgBadge({ id: "r1", changes: { files: 2, add: 3, del: 0, undone: true } });
if (b.indexOf('class="chg-badge undone"') < 0 || b.indexOf("runs.chg_undone_tip") < 0) fail("badge undone", b);
if (runChgBadge({ id: "r1", changes: { files: 2, add: 3, del: 0 } }).indexOf("undone") >= 0) fail("badge", "");
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
