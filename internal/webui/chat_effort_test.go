package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

type fakeEffortChats struct {
	fakeChats
	effort string
	calls  int
}

func (f *fakeEffortChats) SetChatEffort(_ context.Context, id, effort string) (ChatSession, error) {
	f.effort = effort
	f.calls++
	return ChatSession{ID: id, Effort: effort}, f.err
}

// Sessions take an effort on create (absent = the run's value, "" = default)
// and change it with PUT …/effort (login + CSRF); bad values are refused
// before the app sees them.
func TestChatEffortAPI(t *testing.T) {
	c := login(t)
	f := &fakeEffortChats{}
	c.s.deps.ChatSessions = f
	if code, out := c.json(http.MethodPost, "/api/chat-sessions", map[string]any{"origin": "chat", "effort": "high"}); code != 201 || f.created.Effort == nil || *f.created.Effort != "high" {
		t.Fatalf("create %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/chat-sessions", map[string]any{"run_id": "run_1"}); code != 201 || f.created.Effort != nil {
		t.Fatalf("create without effort %d %+v", code, f.created)
	}
	if code, _ := c.json(http.MethodPost, "/api/chat-sessions", map[string]any{"run_id": "run_1", "effort": ""}); code != 201 || f.created.Effort == nil || *f.created.Effort != "" {
		t.Fatalf("create default %d %+v", code, f.created)
	}
	if code, _ := c.json(http.MethodPost, "/api/chat-sessions", map[string]any{"origin": "chat", "effort": "max"}); code != 400 {
		t.Fatalf("bad create effort %d", code)
	}
	for _, e := range []string{"low", "medium", "high", ""} {
		if code, out := c.json(http.MethodPut, "/api/chat-sessions/chat_1/effort", map[string]any{"effort": e}); code != 200 || f.effort != e || out["effort"] != e {
			t.Fatalf("set %q %d %v", e, code, out)
		}
	}
	n := f.calls
	for _, body := range []map[string]any{{"effort": "max"}, {}, {"effort": 3}} {
		if code, _ := c.json(http.MethodPut, "/api/chat-sessions/chat_1/effort", body); code != 400 {
			t.Fatalf("bad body %v: %d", body, code)
		}
	}
	if f.calls != n {
		t.Fatal("bad effort reached the app")
	}
	if code, _ := c.json(http.MethodGet, "/api/chat-sessions/chat_1/effort", nil); code != 405 {
		t.Fatalf("GET effort %d", code)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/chat-sessions/chat_1/effort", strings.NewReader(`{"effort":"low"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 || f.calls != n {
		t.Fatalf("PUT without CSRF %d", rr.Code)
	}
	f.err = ErrChatSessionBusy
	if code, out := c.json(http.MethodPut, "/api/chat-sessions/chat_1/effort", map[string]any{"effort": "low"}); code != 409 || out["code"] != "busy" {
		t.Fatalf("busy %d %v", code, out)
	}
	// an app without the setter: not available
	c.s.deps.ChatSessions = &fakeChats{}
	if code, _ := c.json(http.MethodPut, "/api/chat-sessions/chat_1/effort", map[string]any{"effort": "low"}); code != 404 {
		t.Fatalf("no setter %d", code)
	}
}
