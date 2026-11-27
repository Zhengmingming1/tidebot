package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func insertEmptyChat(t *testing.T, a *App, sid, origin, title, status string, updated time.Time, extras func()) {
	t.Helper()
	ts := updated.UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, title, origin, status, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
		sid, title, origin, status, ts, ts); err != nil {
		t.Fatal(err)
	}
	if extras != nil {
		extras()
	}
}

func sessionExists(t *testing.T, a *App, sid string) bool {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE id=?`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// Unused empty chats older than LogDays are previewed and removed. Sessions the
// user started or kept (a title, turns, bindings, a draft, a proposal, a
// workspace, a continued run, a recent update, or LogDays=0) stay.
func TestEmptyChatSessionRetention(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	day := 24 * time.Hour
	now := time.Now()
	old := now.Add(-40 * day)
	fresh := now.Add(-2 * day)
	policy := RetentionPolicy{LogDays: 30, ArtifactDays: 90}

	insertEmptyChat(t, a, "chat_stale", "chat", "", "idle", old, nil)
	staleDir := filepath.Join(a.chatSessionsDir(), "chat_stale", "work")
	if err := os.MkdirAll(staleDir, 0o700); err != nil {
		t.Fatal(err)
	}

	insertEmptyChat(t, a, "chat_fresh", "chat", "", "idle", fresh, nil)
	insertEmptyChat(t, a, "chat_named", "chat", "以后再用", "idle", old, nil)
	insertEmptyChat(t, a, "chat_from_run", "run", "", "idle", old, nil)
	insertEmptyChat(t, a, "chat_running", "chat", "", "running", old, nil)
	insertEmptyChat(t, a, "chat_titled_ws", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET workspace_dir=? WHERE id='chat_titled_ws'`, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_bound", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET bindings_json='{"skills":["sk_keep"]}' WHERE id='chat_bound'`); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_cap", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET bindings_json='{"capabilities":["cap_keep"]}' WHERE id='chat_cap'`); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_turns", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET turns=1 WHERE id='chat_turns'`); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_turnrow", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES('chat_turnrow',1,'run_x',?)`, old.UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_cost", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET cost_total=0.01 WHERE id='chat_cost'`); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_claude", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET claude_session_id='11111111-1111-1111-1111-111111111111' WHERE id='chat_claude'`); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_draft", "chat", "", "idle", old, func() {
		if err := os.MkdirAll(a.chatDraftsDir("chat_draft"), 0o700); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_proposal", "chat", "", "idle", old, func() {
		if _, err := a.DB.Exec(`INSERT INTO capability_proposals(id,session_id,run_id,kind,payload_json,status,created_at,expires_at,base_version_id)
VALUES('cp_keep','chat_proposal','','install','{}','pending',?,?,'')`, old.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	})
	insertEmptyChat(t, a, "chat_touched", "chat", "", "idle", old, func() {
		// created long ago, but the permission level was changed recently
		if _, err := a.DB.Exec(`UPDATE chat_sessions SET updated_at=? WHERE id='chat_touched'`, fresh.UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	})

	ids, err := a.emptyChatSessions(ctx, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "chat_stale" {
		t.Fatalf("preview ids %v, want only chat_stale", ids)
	}
	v, err := a.StorageUsage(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.Preview.Sessions != 1 {
		t.Fatalf("storage preview sessions %d", v.Preview.Sessions)
	}

	rep, err := a.ApplyRetention(ctx, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedRows != 1 {
		t.Fatalf("deleted rows %+v", rep)
	}
	if sessionExists(t, a, "chat_stale") {
		t.Fatal("stale empty chat kept")
	}
	if _, err := os.Stat(filepath.Join(a.chatSessionsDir(), "chat_stale")); !os.IsNotExist(err) {
		t.Fatal("stale empty chat folder kept")
	}

	keep := []string{"chat_fresh", "chat_named", "chat_from_run", "chat_running", "chat_titled_ws", "chat_bound", "chat_cap", "chat_turns", "chat_turnrow", "chat_cost", "chat_claude", "chat_draft", "chat_proposal", "chat_touched"}
	for _, id := range keep {
		if !sessionExists(t, a, id) {
			t.Fatalf("kept session %s was deleted", id)
		}
	}

	// After cleanup the default-policy preview is empty.
	v, err = a.StorageUsage(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.Preview.Sessions != 0 {
		t.Fatalf("preview still lists %d sessions", v.Preview.Sessions)
	}

	// LogDays=0 never deletes unused empty chats.
	insertEmptyChat(t, a, "chat_forever", "chat", "", "idle", old, nil)
	rep, err = a.ApplyRetention(ctx, RetentionPolicy{LogDays: 0, ArtifactDays: 0}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedRows != 0 || !sessionExists(t, a, "chat_forever") {
		t.Fatalf("LogDays=0 deleted empty chat: %+v", rep)
	}
	ids, err = a.emptyChatSessions(ctx, RetentionPolicy{LogDays: 0}, now)
	if err != nil || len(ids) != 0 {
		t.Fatalf("LogDays=0 preview %v err %v", ids, err)
	}
}

// A turn or title added after the candidate list is built must not be deleted.
func TestEmptyChatSessionRetentionRechecks(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	insertEmptyChat(t, a, "chat_race", "chat", "", "idle", old, nil)
	ids, err := a.emptyChatSessions(ctx, RetentionPolicy{LogDays: 30}, now)
	if err != nil || len(ids) != 1 || ids[0] != "chat_race" {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if _, err := a.DB.Exec(`UPDATE chat_sessions SET title='先留着' WHERE id='chat_race'`); err != nil {
		t.Fatal(err)
	}
	ok, err := a.deleteEmptyChatSession(ctx, "chat_race")
	if err != nil || ok {
		t.Fatalf("deleted a named session: ok=%v err=%v", ok, err)
	}
	if !sessionExists(t, a, "chat_race") {
		t.Fatal("renamed session was deleted")
	}

	insertEmptyChat(t, a, "chat_race2", "chat", "", "idle", old, nil)
	if _, err := a.DB.Exec(`INSERT INTO chat_session_turns(session_id, turn_index, run_id, created_at) VALUES('chat_race2',1,'run_y',?)`, now.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	ok, err = a.deleteEmptyChatSession(ctx, "chat_race2")
	if err != nil || ok || !sessionExists(t, a, "chat_race2") {
		t.Fatalf("deleted a session that had gained a turn: ok=%v err=%v", ok, err)
	}
}

// Creating a blank chat through the API (the New chat button) with default
// bindings still counts as a binding when session defaults are set, and is kept.
func TestEmptyChatSessionRetentionKeepsDefaultBindings(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	old := now.Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`UPDATE chat_sessions SET created_at=?, updated_at=? WHERE id=?`, old, old, ses.ID); err != nil {
		t.Fatal(err)
	}
	// No session defaults: this unused blank chat is eligible.
	ids, err := a.emptyChatSessions(ctx, RetentionPolicy{LogDays: 30}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != ses.ID {
		t.Fatalf("blank chat without defaults should be eligible, got %v", ids)
	}
}

func TestCleanupRecordsEmptyChatSessions(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	insertEmptyChat(t, a, "chat_rec", "chat", "", "idle", old, nil)
	rep, err := a.runCleanup(ctx, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedRows < 1 {
		t.Fatalf("runCleanup report %+v", rep)
	}
	v, err := a.StorageUsage(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.Last == nil || v.Last.DeletedRows < 1 || !v.Last.Auto {
		t.Fatalf("last cleanup %+v", v.Last)
	}
	if sessionExists(t, a, "chat_rec") {
		t.Fatal("session still present after runCleanup")
	}
}
