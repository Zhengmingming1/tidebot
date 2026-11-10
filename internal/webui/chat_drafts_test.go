package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

type fakeDrafts struct {
	fakeChats
	calls   []string
	skill   string
	note    string
	fromRun bool
	derr    error
}

func (f *fakeDrafts) ChatDraft(context.Context, string) (ChatDraft, error) {
	f.calls = append(f.calls, "get")
	return ChatDraft{Active: true, SkillID: "skill_1", SkillName: "<b>x</b>", Adopted: []ChatDraftVersion{}, Skills: []ChatBinding{}}, f.derr
}
func (f *fakeDrafts) StartChatDraft(_ context.Context, _, skill string) (ChatDraft, error) {
	f.calls, f.skill = append(f.calls, "start"), skill
	return ChatDraft{Active: true, SkillID: skill}, f.derr
}
func (f *fakeDrafts) DiscardChatDraft(context.Context, string) (ChatDraft, error) {
	f.calls = append(f.calls, "discard")
	return ChatDraft{}, f.derr
}
func (f *fakeDrafts) SaveChatDraft(_ context.Context, _, note string) (ChatDraft, error) {
	f.calls, f.note = append(f.calls, "save"), note
	return ChatDraft{Active: true, Saved: &ChatDraftVersion{No: 2, Status: "pending"}}, f.derr
}
func (f *fakeDrafts) AdoptChatDraft(context.Context, string) (ChatDraft, error) {
	f.calls = append(f.calls, "adopt")
	return ChatDraft{Active: true}, f.derr
}
func (f *fakeDrafts) ChatTaskPreset(_ context.Context, _ string, fromRun bool) (ChatTaskPreset, error) {
	f.calls, f.fromRun = append(f.calls, "preset"), fromRun
	return ChatTaskPreset{Prompt: "p", RunMode: "exec", SkillBindings: []tasksvc.SkillBinding{{SkillID: "skill_1", Mode: "pinned", VersionID: "ver_2"}}}, f.derr
}

// Draft endpoints: each action reaches the app, errors keep their codes, writes
// need the CSRF header, nothing works signed out.
func TestChatDraftAPI(t *testing.T) {
	c := login(t)
	f := &fakeDrafts{}
	c.s.deps.ChatSessions = f
	base := "/api/chat-sessions/chat_1/"
	if code, out := c.json(http.MethodGet, base+"draft", nil); code != 200 || out["skill_name"] != "<b>x</b>" {
		t.Fatalf("get %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, base+"draft", map[string]any{}); code != 400 {
		t.Fatalf("start without skill %d", code)
	}
	if code, _ := c.json(http.MethodPost, base+"draft", map[string]any{"skill_id": "skill_9"}); code != 200 || f.skill != "skill_9" {
		t.Fatalf("start %d", code)
	}
	if code, out := c.json(http.MethodPost, base+"draft/save", map[string]any{"note": " shorter "}); code != 200 || f.note != "shorter" {
		t.Fatalf("save %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, base+"draft/save", nil); code != 200 {
		t.Fatalf("save without body %d", code)
	}
	if code, _ := c.json(http.MethodPost, base+"draft/adopt", nil); code != 200 {
		t.Fatalf("adopt %d", code)
	}
	if code, _ := c.json(http.MethodDelete, base+"draft", nil); code != 200 {
		t.Fatalf("discard %d", code)
	}
	if code, out := c.json(http.MethodGet, base+"task-preset?from=run", nil); code != 200 || !f.fromRun || out["run_mode"] != "exec" {
		t.Fatalf("preset %d %v", code, out)
	}
	if code, _ := c.json(http.MethodGet, base+"draft/adopt", nil); code != 405 {
		t.Fatalf("GET adopt %d", code)
	}
	for err, want := range map[error]string{ErrChatDraftUnchanged: "draft_unchanged", ErrChatDraftChecks: "draft_checks", ErrChatDraftNone: "no_draft", ErrChatDraftNotSaved: "draft_not_saved", ErrChatSessionBusy: "busy"} {
		f.derr = err
		if _, out := c.json(http.MethodPost, base+"draft/adopt", nil); out["code"] != want {
			t.Fatalf("%v → %v", err, out)
		}
	}
	f.derr = nil

	n := len(f.calls)
	for _, p := range []string{"draft", "draft/save", "draft/adopt"} {
		req := httptest.NewRequest(http.MethodPost, base+p, strings.NewReader(`{"skill_id":"skill_1"}`))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
		rr := httptest.NewRecorder()
		c.s.routes().ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Fatalf("%s without CSRF: %d", p, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, base+"draft", nil))
	if rr.Code != 401 || len(f.calls) != n {
		t.Fatalf("signed out %d %v", rr.Code, f.calls)
	}
	c.s.deps.ChatSessions = &fakeChats{}
	if code, _ := c.json(http.MethodGet, base+"draft", nil); code != 404 {
		t.Fatalf("no drafts in the app: %d", code)
	}
}

// Task files carry the permission level and effort; an imported command task
// says so in the preview; an older file without them imports as before.
func TestTaskTransferRunMode(t *testing.T) {
	c := login(t)
	code, src := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "抓取", "prompt": "下载数据并统计", "provider": "anthropic", "run_mode": "exec", "effort": "high"})
	if code != 201 {
		t.Fatalf("create %d %v", code, src)
	}
	rr := c.do(http.MethodGet, "/api/tasks/export?ids="+src["id"].(string), "", nil)
	var f taskFile
	if err := json.Unmarshal(rr.Body.Bytes(), &f); err != nil || len(f.Tasks) != 1 || f.Tasks[0].RunMode != "exec" || f.Tasks[0].Effort != "high" {
		t.Fatalf("export %v %s", err, rr.Body.String())
	}
	raw, _ := json.Marshal(f)
	code, out := c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": json.RawMessage(raw), "apply": true})
	if code != 200 {
		t.Fatalf("import %d %v", code, out)
	}
	row := out["tasks"].([]any)[0].(map[string]any)
	notes, _ := json.Marshal(row["notes"])
	if !strings.Contains(string(notes), "run_exec") || row["task_id"] == nil {
		t.Fatalf("import row %v", row)
	}
	_, form := c.json(http.MethodGet, "/api/tasks/"+row["task_id"].(string)+"/form", nil)
	if fm := form["form"].(map[string]any); fm["run_mode"] != "exec" || fm["effort"] != "high" {
		t.Fatalf("imported form %v", fm)
	}
	f.Tasks[0].RunMode, f.Tasks[0].Effort = "root", ""
	raw, _ = json.Marshal(f)
	if _, out = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": json.RawMessage(raw)}); out["tasks"].([]any)[0].(map[string]any)["error"] == nil {
		t.Fatalf("bad run mode imported %v", out)
	}
	f.Tasks[0].RunMode = ""
	raw, _ = json.Marshal(f)
	_, out = c.json(http.MethodPost, "/api/tasks/import", map[string]any{"file": json.RawMessage(raw), "apply": true})
	row = out["tasks"].([]any)[0].(map[string]any)
	_, form = c.json(http.MethodGet, "/api/tasks/"+row["task_id"].(string)+"/form", nil)
	if fm := form["form"].(map[string]any); fm["run_mode"] != "" && fm["run_mode"] != nil {
		t.Fatalf("older file %v", fm)
	}
}
