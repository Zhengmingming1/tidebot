package webui

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

type fakeModeChats struct {
	fakeChats
	mode string
}

func (f *fakeModeChats) SetChatMode(_ context.Context, id, mode string) (ChatSession, error) {
	f.mode = mode
	return ChatSession{ID: id, PermissionMode: mode}, f.err
}

// The three permission levels: the Run dialog accepts exec, sessions take a level
// on create and change it (login + CSRF), tasks save run_mode and effort.
func TestPermissionModesAPI(t *testing.T) {
	for _, m := range []string{"", "read", "edit", "exec"} {
		r := RunRequest{Prompt: "x", Mode: m}
		if msg := r.validate(); msg != "" {
			t.Fatalf("mode %q: %s", m, msg)
		}
	}
	if r := (RunRequest{Prompt: "x", Mode: "root"}); r.validate() == "" {
		t.Fatal("bad mode accepted")
	}
	if err := (DependencyCheckRequest{Kind: "draft", Prompt: "x", Mode: "exec"}).Validate(); err != nil {
		t.Fatal(err)
	}

	c := login(t)
	f := &fakeModeChats{}
	c.s.deps.ChatSessions = f
	if code, out := c.json(http.MethodPost, "/api/chat-sessions", map[string]any{"origin": "chat", "permission_mode": "exec"}); code != 201 || f.created.PermissionMode != "exec" {
		t.Fatalf("create %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/chat-sessions", map[string]any{"origin": "chat", "permission_mode": "root"}); code != 400 {
		t.Fatalf("bad create mode %d", code)
	}
	if code, out := c.json(http.MethodPut, "/api/chat-sessions/chat_1/mode", map[string]any{"mode": "edit"}); code != 200 || f.mode != "edit" || out["permission_mode"] != "edit" {
		t.Fatalf("set mode %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPut, "/api/chat-sessions/chat_1/mode", map[string]any{"mode": "sudo"}); code != 400 {
		t.Fatalf("bad mode %d", code)
	}
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_1/mode", nil); code != 405 {
		t.Fatalf("GET mode %d", code)
	}
	f.err = ErrChatSessionBusy
	if code, out := c.json(http.MethodPut, "/api/chat-sessions/chat_1/mode", map[string]any{"mode": "read"}); code != 409 || out["code"] != "busy" {
		t.Fatalf("busy %d %v", code, out)
	}

	base := map[string]any{"name": "Fetch", "prompt": "fetch the feed", "provider": "anthropic", "enabled": false, "run_mode": "exec", "effort": "high"}
	code, tk := c.json(http.MethodPost, "/api/tasks", base)
	if code != 201 {
		t.Fatalf("task %d %v", code, tk)
	}
	id := tk["id"].(string)
	_, form := c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	fm := form["form"].(map[string]any)
	if fm["run_mode"] != "exec" || fm["effort"] != "high" {
		t.Fatalf("form %v", fm)
	}
	base["run_mode"] = "root"
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, base); code != 400 {
		t.Fatalf("bad run mode saved %d", code)
	}
	base["run_mode"], base["effort"] = "read", "max"
	if code, _ := c.json(http.MethodPut, "/api/tasks/"+id, base); code != 400 {
		t.Fatalf("bad effort saved %d", code)
	}
	// an older page without the fields keeps them
	delete(base, "run_mode")
	delete(base, "effort")
	if code, out := c.json(http.MethodPut, "/api/tasks/"+id, base); code != 200 {
		t.Fatalf("older page %d %v", code, out)
	}
	_, form = c.json(http.MethodGet, "/api/tasks/"+id+"/form", nil)
	if fm = form["form"].(map[string]any); fm["run_mode"] != "exec" || fm["effort"] != "high" {
		t.Fatalf("kept %v", fm)
	}
}

// A command run without a folder ran in tidebot's scratch folder: the run list
// doesn't show that path, and re-running starts it without one.
func TestExecScratchRunHidesFolder(t *testing.T) {
	c := login(t)
	c.s.deps.Cfg.HomeDir = t.TempDir()
	c.s.deps.Cfg.DataDir = filepath.Join(c.s.deps.Cfg.HomeDir, "data")
	seedRun(t, c, "ex1", "sum it", "succeeded", 0.01, time.Now())
	scratch := filepath.Join(c.s.deps.Cfg.DataDir, "run-work", "run-1")
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET trigger_kind='quick', run_mode='exec', workspace_dir=? WHERE id='ex1'`, scratch); err != nil {
		t.Fatal(err)
	}
	runs := listRuns(t, c, "")
	if len(runs) != 1 || runs[0]["workspace"] != nil && runs[0]["workspace"] != "" || runs[0]["run_mode"] != "exec" {
		t.Fatalf("runs %v", runs)
	}
	var got RunRequest
	c.s.deps.StartRun = func(r RunRequest) (string, error) { got = r; return "run_new", nil }
	if code, out := c.json(http.MethodPost, "/api/runs/ex1/rerun", map[string]any{}); code >= 300 || got.Workspace != "" || got.Mode != "exec" {
		t.Fatalf("rerun %d %v %+v", code, out, got)
	}
}
