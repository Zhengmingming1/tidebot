package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// POST /api/runs/quick: a skill or a folder goes to StartRun with the dialog's
// fields; a plain prompt still goes to QuickRun. Folder problems come back with
// their code, a busy folder is 409, and the CSRF token is required.
func TestRunDialogStart(t *testing.T) {
	c := login(t)
	var got []RunRequest
	quick := 0
	c.s.deps.QuickRun = func(p, prov string) (string, error) { quick++; return "", nil }
	c.s.deps.StartRun = func(req RunRequest) (string, error) {
		got = append(got, req)
		switch req.Workspace {
		case "/etc":
			return "", &guardDirError
		case "/busy":
			return "", errBusyForTest
		}
		return "", nil
	}
	if code, _ := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "hi"}); code != 202 || quick != 1 || len(got) != 0 {
		t.Fatalf("plain prompt: %d quick=%d start=%d", code, quick, len(got))
	}
	body := map[string]any{"prompt": " review ", "skill_id": "skl_1", "workspace": "/code/p", "mode": "edit", "provider": "kimi"}
	if code, _ := c.json(http.MethodPost, "/api/runs/quick", body); code != 202 || len(got) != 1 {
		t.Fatalf("skill run: %d %v", code, got)
	}
	if g := got[0]; g.Prompt != "review" || g.SkillID != "skl_1" || g.Workspace != "/code/p" || g.Mode != "edit" || g.Provider != "kimi" {
		t.Fatalf("%+v", g)
	}
	if code, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "x", "workspace": "/etc"}); code != 400 || out["problem"] != "sensitive" {
		t.Fatalf("sensitive: %d %v", code, out)
	}
	if code, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "x", "workspace": "/busy", "mode": "edit"}); code != 409 || out["problem"] != "busy" {
		t.Fatalf("busy: %d %v", code, out)
	}
	for _, bad := range []map[string]any{
		{"prompt": "x", "skill_id": "../../etc"},
		{"prompt": "x", "workspace": "/a", "mode": "root"},
		{"prompt": strings.Repeat("长", runPromptMax+1), "workspace": "/a"},
	} {
		if code, _ := c.json(http.MethodPost, "/api/runs/quick", bad); code != 400 {
			t.Fatalf("%v accepted: %d", bad, code)
		}
	}
	n := len(got)
	req := httptest.NewRequest(http.MethodPost, "/api/runs/quick", strings.NewReader(`{"prompt":"x","workspace":"/code/p"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 || len(got) != n {
		t.Fatalf("no CSRF: %d", rr.Code)
	}
}

// workspace-check?mode=run reports git state without touching the repository,
// and refuses sensitive folders.
func TestRunDirCheck(t *testing.T) {
	c := login(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	repo := filepath.Join(home, "proj")
	_ = os.MkdirAll(repo, 0o755)
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "init", "-q", "-b", "main")
		cmd.Dir = repo
		if err := cmd.Run(); err != nil {
			t.Skip("git init:", err)
		}
	}
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b"), 0o644)
	before := treeState(t, repo)
	code, out := c.json(http.MethodGet, "/api/tasks/workspace-check?mode=run&path="+urlQ(repo), nil)
	if code != 200 || out["problem"] != "" || out["exists"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if g, ok := out["git"].(map[string]any); ok {
		if g["branch"] != "main" || g["dirty"] != float64(2) {
			t.Fatalf("git: %v", g)
		}
	} else if _, err := exec.LookPath("git"); err == nil {
		t.Fatalf("no git state: %v", out)
	}
	if after := treeState(t, repo); after != before {
		t.Fatalf("check changed the repo:\n%s\n---\n%s", before, after)
	}
	for p, want := range map[string]string{filepath.Join(home, ".ssh"): "sensitive", home: "contains_sensitive", "~": "contains_sensitive", "rel": "relative", filepath.Join(home, "nope"): "not_found"} {
		if _, out := c.json(http.MethodGet, "/api/tasks/workspace-check?mode=run&path="+urlQ(p), nil); out["problem"] != want {
			t.Errorf("%s: %v, want %s", p, out["problem"], want)
		}
	}
}

func treeState(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(root, p)
			data := ""
			if !fi.IsDir() {
				c, _ := os.ReadFile(p)
				data = string(c)
			}
			b.WriteString(rel + " " + fi.Mode().String() + " " + fi.ModTime().String() + " " + data + "\n")
		}
		return nil
	})
	return b.String()
}

var guardDirError = guard.DirError{Problem: guard.DirSensitive}
var errBusyForTest = guard.ErrDirBusy

func urlQ(s string) string { return url.QueryEscape(s) }

// The Run dialog's wiring lives in app.js; these are the pieces that make it a
// skill + folder runner rather than the old prompt-only quick run.
func TestRunDialogUI(t *testing.T) {
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"/api/tasks/workspace-check?mode=run&path="`,
		`"/api/runs/defaults"`,
		`data-sk-run="`,
		`T("pal.run_skill"`,
		`name="mode" value="edit"`,
		`T("rn.hint_dirty", { n: g.dirty })`,
		`r.task_name || r.skill_name || T("trig.quick")`,
		`function runCtxText(r, short)`,
		`setWs(l.workspace); setMode(l.mode); drawDirs();`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	for _, want := range []string{".modal:has(.rn-form)", ".rn-mode label[data-tip]:hover::after", ".rn-hint.warn"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lost %q", want)
		}
	}
}
