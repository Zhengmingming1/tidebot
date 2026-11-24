package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// Session times use the capability layout (fixed 9-digit fraction), so the
// session list (ORDER BY updated_at DESC as text) follows time order inside one
// second, also next to updated_at written by a capability install.
func TestChatSessionFixedWidthTimes(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	fixed := func(what, v string) {
		t.Helper()
		if len(v) != len(capability.TimeLayout) {
			t.Fatalf("%s %q is not in the fixed layout", what, v)
		}
		if _, err := time.Parse(capability.TimeLayout, v); err != nil {
			t.Fatalf("%s %q: %v", what, v, err)
		}
	}
	first := func() string {
		t.Helper()
		list, err := a.ListChatSessions(ctx, 10)
		if err != nil || len(list) == 0 {
			t.Fatalf("list: %v %v", list, err)
		}
		fixed("listed updated_at", list[0].UpdatedAt)
		return list[0].ID
	}
	older, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // a later id, usually in the same second
	newer, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	fixed("created_at", older.CreatedAt)
	fixed("updated_at", older.UpdatedAt)
	if got := first(); got != newer.ID {
		t.Fatalf("newest session first: got %s", got)
	}
	// touching the older session moves it up, even within the same second
	// (whole-second stamps tied and fell back to id order)
	time.Sleep(2 * time.Millisecond)
	if _, err := a.SetChatMode(ctx, older.ID, "edit"); err != nil {
		t.Fatal(err)
	}
	if got := first(); got != older.ID {
		t.Fatalf("session changed last should be first: got %s", got)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := a.SetChatEffort(ctx, newer.ID, "low"); err != nil {
		t.Fatal(err)
	}
	if got := first(); got != newer.ID {
		t.Fatalf("session changed last should be first: got %s", got)
	}
	// a turn stamps the session and its turn row the same way
	time.Sleep(2 * time.Millisecond)
	if _, err := a.SendChatTurn(ctx, older.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, older.ID, 1)
	if got := first(); got != older.ID {
		t.Fatalf("session with the latest turn should be first: got %s", got)
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT created_at FROM chat_session_turns WHERE session_id=?`, older.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		fixed("turn created_at", v)
		n++
	}
	if n == 0 {
		t.Fatal("no turn rows")
	}
}
