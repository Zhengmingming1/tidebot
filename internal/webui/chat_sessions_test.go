package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

type fakeChats struct {
	created ChatSessionCreate
	sent    string
	bound   ChatBindingsInput
	err     error
}

func (f *fakeChats) CreateChatSession(_ context.Context, req ChatSessionCreate) (ChatSession, error) {
	f.created = req
	return ChatSession{ID: "chat_1", Title: "<script>x</script>", OriginRunID: req.RunID, Status: "idle"}, f.err
}
func (f *fakeChats) ListChatSessions(context.Context, int) ([]ChatSession, error) { return nil, f.err }
func (f *fakeChats) GetChatSession(_ context.Context, id string) (ChatSessionDetail, error) {
	if id != "chat_1" {
		return ChatSessionDetail{}, ErrChatSessionNotFound
	}
	return ChatSessionDetail{ChatSession: ChatSession{ID: id}, TurnList: []ChatTurn{{Index: 0, RunID: "run_a", Answer: "<b>hi</b>"}}}, f.err
}
func (f *fakeChats) SendChatTurn(_ context.Context, id, msg string) (ChatTurnStarted, error) {
	f.sent = msg
	return ChatTurnStarted{Index: 1, RunID: "run_b"}, f.err
}
func (f *fakeChats) DeleteChatSession(context.Context, string) error { return f.err }
func (f *fakeChats) SetChatBindings(_ context.Context, id string, in ChatBindingsInput) (ChatSession, error) {
	f.bound = in
	return ChatSession{ID: id, Bindings: []ChatBinding{{Kind: "skill", ID: "sk_1", Name: "<i>x</i>"}}}, f.err
}

func TestChatSessionBindingsAPI(t *testing.T) {
	c := login(t)
	f := &fakeChats{}
	c.s.deps.ChatSessions = f
	rr := c.do(http.MethodPut, "/api/chat-sessions/chat_1/bindings", "application/json", []byte(`{"skills":["sk_1"],"capabilities":["cap_a"]}`))
	if rr.Code != 200 || len(f.bound.Skills) != 1 || f.bound.Capabilities[0] != "cap_a" || strings.Contains(rr.Body.String(), "<i>") {
		t.Fatalf("bind %d %s %+v", rr.Code, rr.Body, f.bound)
	}
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_1/bindings", nil); code != 405 {
		t.Fatalf("GET bindings %d", code)
	}
	if rr := c.do(http.MethodPut, "/api/chat-sessions/chat_1/bindings", "application/json", []byte(`{`)); rr.Code != 400 {
		t.Fatalf("bad body %d", rr.Code)
	}
	f.err = ErrChatInvalid
	if code, out := c.json(http.MethodPut, "/api/chat-sessions/chat_1/bindings", map[string]any{"skills": []string{"nope"}}); code != 400 || out["code"] != "invalid" {
		t.Fatalf("unknown skill %d %v", code, out)
	}
	// no CSRF token: refused
	req := httptest.NewRequest(http.MethodPut, "/api/chat-sessions/chat_1/bindings", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	w := httptest.NewRecorder()
	c.s.routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("no csrf %d", w.Code)
	}
}

func TestChatSessionAPI(t *testing.T) {
	c := login(t)
	f := &fakeChats{}
	c.s.deps.ChatSessions = f

	code, out := c.json(http.MethodGet, "/api/chat-sessions", nil)
	if code != 200 || out["sessions"] == nil {
		t.Fatalf("list %d %v", code, out)
	}
	rr := c.do(http.MethodPost, "/api/chat-sessions", "application/json", []byte(`{"run_id":"run_a","web_access":true}`))
	if rr.Code != 201 || f.created.RunID != "run_a" || !f.created.WebAccess {
		t.Fatalf("create %d %s %+v", rr.Code, rr.Body, f.created)
	}
	if strings.Contains(rr.Body.String(), "<script>") || !strings.Contains(rr.Body.String(), `\u003cscript\u003e`) {
		t.Fatalf("output not escaped: %s", rr.Body)
	}
	if code, out := c.json(http.MethodPost, "/api/chat-sessions", map[string]string{}); code != 400 || out["code"] != "invalid" {
		t.Fatalf("no run id %d %v", code, out)
	}
	// a blank session: origin "chat" and no run; other origins are refused
	if code, _ := c.json(http.MethodPost, "/api/chat-sessions", map[string]string{"origin": "chat"}); code != 201 || f.created.Origin != "chat" || f.created.RunID != "" {
		t.Fatalf("blank %d %+v", code, f.created)
	}
	for _, body := range []map[string]string{{"origin": "run"}, {"origin": "run_fix", "run_id": "run_a"}, {"origin": "x"}} {
		if code, out := c.json(http.MethodPost, "/api/chat-sessions", body); code != 400 || out["code"] != "invalid" {
			t.Fatalf("%v: %d %v", body, code, out)
		}
	}
	if rr := c.do(http.MethodGet, "/api/chat-sessions/chat_1", "", nil); rr.Code != 200 || strings.Contains(rr.Body.String(), "<b>") {
		t.Fatalf("get %d %s", rr.Code, rr.Body)
	}
	if code, out := c.json(http.MethodGet, "/api/chat-sessions/chat_x", nil); code != 404 || out["code"] != "not_found" {
		t.Fatalf("unknown %d %v", code, out)
	}
	if code, out := c.json(http.MethodPost, "/api/chat-sessions/chat_1/turns", map[string]string{"message": "next?"}); code != 202 || out["run_id"] != "run_b" || f.sent != "next?" {
		t.Fatalf("send %d %v", code, out)
	}
	if code, out := c.json(http.MethodPost, "/api/chat-sessions/chat_1/turns", map[string]string{"message": strings.Repeat("x", ChatMessageMax+1)}); code != 400 || out["code"] != "too_long" {
		t.Fatalf("long %d %v", code, out)
	}
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_1/turns", nil); code != 405 {
		t.Fatalf("GET turns %d", code)
	}
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_1/other", nil); code != 404 {
		t.Fatalf("unknown sub %d", code)
	}
	for _, e := range []struct {
		err  error
		code int
		key  string
	}{{ErrChatSessionBusy, 409, "busy"}, {ErrChatTurnLimit, 409, "turn_limit"}, {ErrChatRunNotFinished, 409, "run_not_finished"}, {ErrChatRunNotFound, 404, "run_not_found"}} {
		f.err = e.err
		if code, out := c.json(http.MethodPost, "/api/chat-sessions/chat_1/turns", map[string]string{"message": "m"}); code != e.code || out["code"] != e.key {
			t.Fatalf("%v: %d %v", e.err, code, out)
		}
	}
	f.err = nil
	if code, _ := c.json(http.MethodDelete, "/api/chat-sessions/chat_1", nil); code != 200 {
		t.Fatalf("delete %d", code)
	}

	// Writes need the CSRF header; nothing works signed out.
	req := httptest.NewRequest(http.MethodPost, "/api/chat-sessions/chat_1/turns", strings.NewReader(`{"message":"m"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr = httptest.NewRecorder()
	f.sent = ""
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 || f.sent != "" {
		t.Fatalf("no CSRF: %d sent=%q", rr.Code, f.sent)
	}
	rr = httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/chat-sessions", nil))
	if rr.Code != 401 {
		t.Fatalf("signed out: %d", rr.Code)
	}

	c.s.deps.ChatSessions = nil
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions", nil); code != 404 {
		t.Fatalf("not wired: %d", code)
	}
}
