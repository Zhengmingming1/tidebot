package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// TestMissingWorkspacesAPI: after a restore flag is set, GET lists live tasks
// whose current working folder is missing and not under the tidebot home;
// default / existing / in-home / archived folders stay off the list; the
// current version is preferred when it names a folder; versions are not
// rewritten; an empty list auto-dismisses; POST hides the notice until the
// next restore.
func TestMissingWorkspacesAPI(t *testing.T) {
	c := login(t)
	home := t.TempDir()
	c.s.deps.Cfg.HomeDir = home
	c.s.deps.Cfg.DataDir = filepath.Join(home, "data")
	ctx := context.Background()

	code, out := c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if code != 200 || out["pending"] != false {
		t.Fatalf("no restore: %d %v", code, out)
	}
	if _, ok := out["tasks"].([]any); !ok {
		t.Fatal("tasks should be an array")
	}

	here := t.TempDir()
	gone := filepath.Join(t.TempDir(), "old-machine", "app")
	otherGone := filepath.Join(t.TempDir(), "old-machine", "docs")
	inHome := filepath.Join(home, "workspaces", "task-default")

	create := func(name, ws string) string {
		t.Helper()
		body := map[string]any{"name": name, "prompt": "p", "provider": "anthropic"}
		if ws != "" {
			body["workspace"] = ws
		}
		code, tk := c.json(http.MethodPost, "/api/tasks", body)
		id, _ := tk["id"].(string)
		if code != 201 || id == "" {
			t.Fatalf("create %s: %d %v", name, code, tk)
		}
		return id
	}
	defID := create("Default folder", "")
	okID := create("Still here", here)
	missID := create("Blog <app>", gone)
	otherID := create("Notes", otherGone)
	archID := create("Archived gone", gone)
	if code, _ := c.json(http.MethodDelete, "/api/tasks/"+archID, nil); code >= 300 {
		t.Fatalf("archive %d", code)
	}

	// A remapped default folder: the tasks row points under the new home, which
	// does not exist yet but will be created on the first run.
	if _, err := c.s.deps.DB.Exec(`UPDATE tasks SET workspace_dir=? WHERE id=?`, inHome, defID); err != nil {
		t.Fatal(err)
	}

	// Prefer the current version's folder when it disagrees with the tasks row
	// (the same rule a run uses). Leave the version hash alone; we only read.
	if _, err := c.s.deps.DB.Exec(`UPDATE versions SET content_json=json_set(content_json,'$.workspace_dir',?)
WHERE id=(SELECT version_id FROM pointers WHERE entity_type='task' AND entity_id=? AND name='current')`, gone, okID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := c.s.deps.DB.QueryRow(`SELECT v.content_json FROM versions v JOIN pointers p ON p.version_id=v.id
WHERE p.entity_type='task' AND p.entity_id=? AND p.name='current'`, okID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	if err := MarkMissingWorkspaceNotice(ctx, c.s.deps.DB); err != nil {
		t.Fatal(err)
	}

	code, out = c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if code != 200 || out["pending"] != true {
		t.Fatalf("pending list: %d %v", code, out)
	}
	got := map[string]map[string]any{}
	for _, raw := range out["tasks"].([]any) {
		row := raw.(map[string]any)
		got[row["id"].(string)] = row
	}
	if _, ok := got[defID]; ok {
		t.Errorf("default / in-home folder listed: %v", got[defID])
	}
	if _, ok := got[archID]; ok {
		t.Errorf("archived task listed: %v", got[archID])
	}
	if row := got[missID]; row == nil || row["name"] != "Blog <app>" || row["workspace_dir"] != gone {
		t.Errorf("missing user folder: %v", row)
	}
	if row := got[otherID]; row == nil || row["workspace_dir"] != otherGone {
		t.Errorf("second missing folder: %v", row)
	}
	if row := got[okID]; row == nil || row["workspace_dir"] != gone {
		t.Errorf("current version folder should win: %v", row)
	}
	if len(got) != 3 {
		t.Errorf("listed %d tasks, want 3: %v", len(got), got)
	}

	var after string
	if err := c.s.deps.DB.QueryRow(`SELECT v.content_json FROM versions v JOIN pointers p ON p.version_id=v.id
WHERE p.entity_type='task' AND p.entity_id=? AND p.name='current'`, okID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("version content rewritten:\n%s\n%s", before, after)
	}

	code, out = c.json(http.MethodPost, "/api/tasks/missing-workspaces", map[string]any{})
	if code != 200 || out["pending"] != false {
		t.Fatalf("dismiss: %d %v", code, out)
	}
	code, out = c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if code != 200 || out["pending"] != false {
		t.Fatalf("after dismiss: %d %v", code, out)
	}
	if tasks, _ := out["tasks"].([]any); len(tasks) != 0 {
		t.Fatalf("dismissed list %v", tasks)
	}
	if err := c.s.deps.DB.QueryRow(`SELECT v.content_json FROM versions v JOIN pointers p ON p.version_id=v.id
WHERE p.entity_type='task' AND p.entity_id=? AND p.name='current'`, okID).Scan(&after); err != nil || after != before {
		t.Fatalf("dismiss rewrote versions: %s %v", after, err)
	}

	if code, _ = c.json(http.MethodPost, "/api/tasks/missing-workspaces", map[string]any{}); code != 200 {
		t.Fatalf("second dismiss %d", code)
	}
	if code, _ = c.json(http.MethodPut, "/api/tasks/missing-workspaces", map[string]any{}); code != 405 {
		t.Fatalf("PUT %d", code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/missing-workspaces", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("POST without CSRF %d", rr.Code)
	}

	// A later restore shows the notice again. If every folder now exists, GET
	// clears the flag itself.
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherGone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MarkMissingWorkspaceNotice(ctx, c.s.deps.DB); err != nil {
		t.Fatal(err)
	}
	code, out = c.json(http.MethodGet, "/api/tasks/missing-workspaces", nil)
	if code != 200 || out["pending"] != false {
		t.Fatalf("auto-dismiss when all exist: %d %v", code, out)
	}
	pending, err := missingWorkspaceNoticePending(ctx, c.s.deps.DB)
	if err != nil || pending {
		t.Fatalf("flag still pending: %v %v", pending, err)
	}
}
