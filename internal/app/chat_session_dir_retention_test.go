package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func placeSessionDir(t *testing.T, a *App, sid string, size int) string {
	t.Helper()
	dir := filepath.Join(a.chatSessionsDir(), sid)
	for _, sub := range []string{"work", "draft"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "f.bin"), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func insertChatRow(t *testing.T, a *App, sid string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, status, created_at, updated_at) VALUES(?,?,?,?)`, sid, "idle", now, now); err != nil {
		t.Fatal(err)
	}
}

// A deleted session's folder (scratch + draft) that stayed on disk is found by
// cleanup, counted in the preview and removed; live sessions and odd entries stay.
func TestChatSessionDirRetentionOrphans(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	orphan := placeSessionDir(t, a, "chat_orphan1", 32<<10)
	live := placeSessionDir(t, a, "chat_live1", 8<<10)
	insertChatRow(t, a, "chat_live1")
	odd := filepath.Join(a.chatSessionsDir(), "not a session")
	if err := os.MkdirAll(odd, 0o700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(a.chatSessionsDir(), "note.txt")
	if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Preview.Files != 1 || v.Preview.Bytes != 64<<10 {
		t.Fatalf("preview %+v, want 1 folder of %d bytes", v.Preview, 64<<10)
	}
	rep, err := a.ApplyRetention(ctx, a.RetentionSettings(ctx), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedFiles != 1 || rep.FreedBytes != 64<<10 {
		t.Fatalf("report %+v", rep)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan session folder kept")
	}
	for _, p := range []string{live, odd, stray} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s removed: %v", p, err)
		}
	}
	// logs+artifacts total is not inflated by session folders
	if rep.TotalBytes != 0 {
		t.Fatalf("total %d", rep.TotalBytes)
	}
}

// A folder that can't be removed right now doesn't fail the delete (the row is
// already gone); the next cleanup removes it.
func TestDeleteChatSessionFolderLeftForCleanup(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the owner can't empty (unix, not root)")
	}
	a := newTestApp(t)
	ctx := context.Background()
	dir := placeSessionDir(t, a, "chat_held1", 4<<10)
	insertChatRow(t, a, "chat_held1")
	work := filepath.Join(dir, "work")
	if err := os.Chmod(work, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(work, 0o700) })

	if err := a.DeleteChatSession(ctx, "chat_held1"); err != nil {
		t.Fatalf("delete reported %v after the session was removed", err)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE id=?`, "chat_held1").Scan(&n)
	if n != 0 {
		t.Fatal("session row kept")
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("expected the held folder to stay for this test: %v", err)
	}
	_ = os.Chmod(work, 0o700) // the file is no longer held
	rep, err := a.ApplyRetention(ctx, a.RetentionSettings(ctx), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedFiles != 1 {
		t.Fatalf("report %+v", rep)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("left-over session folder not cleaned up")
	}
}

// A draft started while the session is being deleted can't recreate its folder.
func TestDeleteChatSessionWaitsForDraftLock(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	insertChatRow(t, a, "chat_lock1")
	chatDraftMu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.DeleteChatSession(ctx, "chat_lock1") }()
	select {
	case err := <-done:
		chatDraftMu.Unlock()
		t.Fatalf("delete ran while a draft change held the lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	chatDraftMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartChatDraft(ctx, "chat_lock1", "sk_x"); err == nil {
		t.Fatal("draft started on a deleted session")
	}
	if _, err := os.Stat(filepath.Join(a.chatSessionsDir(), "chat_lock1")); !os.IsNotExist(err) {
		t.Fatal("deleted session's folder recreated")
	}
}
