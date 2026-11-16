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

func insertChatRowAt(t *testing.T, a *App, sid, status string, updated time.Time) {
	t.Helper()
	ts := updated.UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, status, created_at, updated_at) VALUES(?,?,?,?)`, sid, status, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func sessionSubSize(t *testing.T, dir, sub string) int64 {
	t.Helper()
	n, _ := dirSize(filepath.Join(dir, sub))
	return n
}

// A live session idle longer than LogDays has only its scratch folder cleared.
// The session row, skill draft, fresh sessions, running sessions, and LogDays=0
// keep their work folders. Next turn can recreate the scratch folder.
func TestChatSessionDirRetentionIdleWork(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	day := 24 * time.Hour
	now := time.Now()
	policy := RetentionPolicy{LogDays: 30, ArtifactDays: 90}

	idleWork, idleDraft := 1<<20, 256<<10 // 1 MiB scratch, 256 KiB draft
	idleDir := filepath.Join(a.chatSessionsDir(), "chat_idle_w")
	for sub, size := range map[string]int{"work": idleWork, "draft": idleDraft} {
		if err := os.MkdirAll(filepath.Join(idleDir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(idleDir, sub, "f.bin"), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	insertChatRowAt(t, a, "chat_idle_w", "idle", now.Add(-40*day))

	freshDir := placeSessionDir(t, a, "chat_fresh_w", 8<<10)
	insertChatRowAt(t, a, "chat_fresh_w", "idle", now.Add(-2*day))

	runDir := placeSessionDir(t, a, "chat_run_w", 8<<10)
	insertChatRowAt(t, a, "chat_run_w", "running", now.Add(-40*day))

	closedDir := filepath.Join(a.chatSessionsDir(), "chat_closed_w")
	if err := os.MkdirAll(filepath.Join(closedDir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(closedDir, "work", "f.bin"), make([]byte, 32<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	insertChatRowAt(t, a, "chat_closed_w", "closed", now.Add(-40*day))

	badStampDir := placeSessionDir(t, a, "chat_badstamp_w", 8<<10)
	if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, status, created_at, updated_at) VALUES(?,?,?,?)`,
		"chat_badstamp_w", "idle", "not-a-time", "not-a-time"); err != nil {
		t.Fatal(err)
	}

	emptyDir := filepath.Join(a.chatSessionsDir(), "chat_empty_w")
	if err := os.MkdirAll(filepath.Join(emptyDir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	insertChatRowAt(t, a, "chat_empty_w", "idle", now.Add(-40*day))

	// Preview under LogDays=30 includes idle + closed work, not drafts or others.
	v, err := a.StorageUsage(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a.planRetention(ctx, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var planned int64
	for _, f := range plan.del {
		got[f.runID] = f.kind
		if f.kind == "session-work" {
			planned += f.size
		}
	}
	if got["chat_idle_w"] != "session-work" || got["chat_closed_w"] != "session-work" {
		t.Fatalf("planned %+v", plan.del)
	}
	for _, id := range []string{"chat_fresh_w", "chat_run_w", "chat_badstamp_w", "chat_empty_w"} {
		if _, ok := got[id]; ok {
			t.Fatalf("unexpected plan entry %s: %+v", id, plan.del)
		}
	}
	wantFree := int64(idleWork + (32 << 10))
	if planned != wantFree {
		t.Fatalf("planned work bytes %d, want %d", planned, wantFree)
	}
	if v.Preview.Bytes < wantFree {
		t.Fatalf("usage preview %+v, want at least %d", v.Preview, wantFree)
	}

	beforeIdle, _ := dirSize(idleDir)
	t.Logf("before: idle session folder %d bytes (work %d, draft %d)", beforeIdle, idleWork, idleDraft)

	rep, err := a.ApplyRetention(ctx, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedFiles != 2 || rep.FreedBytes != wantFree {
		t.Fatalf("report %+v, want 2 files / %d bytes", rep, wantFree)
	}
	afterIdle, _ := dirSize(idleDir)
	t.Logf("after: idle session folder %d bytes (work gone, draft %d); freed %d", afterIdle, sessionSubSize(t, idleDir, "draft"), rep.FreedBytes)
	if afterIdle != int64(idleDraft) {
		t.Fatalf("idle folder after cleanup %d, want draft only %d", afterIdle, idleDraft)
	}
	if _, err := os.Stat(filepath.Join(idleDir, "work")); !os.IsNotExist(err) {
		t.Fatal("idle work folder kept")
	}
	if _, err := os.Stat(filepath.Join(idleDir, "draft", "f.bin")); err != nil {
		t.Fatalf("idle draft removed: %v", err)
	}
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE id='chat_idle_w'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("idle session row lost: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(closedDir, "work")); !os.IsNotExist(err) {
		t.Fatal("closed session work kept")
	}
	for _, dir := range []string{freshDir, runDir, badStampDir} {
		if _, err := os.Stat(filepath.Join(dir, "work", "f.bin")); err != nil {
			t.Fatalf("%s work removed: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(emptyDir, "work")); err != nil {
		t.Fatalf("empty work dir removed: %v", err)
	}

	// LogDays=0 keeps idle work forever (orphans would still go).
	keepDir := placeSessionDir(t, a, "chat_keep0_w", 8<<10)
	insertChatRowAt(t, a, "chat_keep0_w", "idle", now.Add(-90*day))
	rep, err = a.ApplyRetention(ctx, RetentionPolicy{LogDays: 0, ArtifactDays: 0}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedFiles != 0 {
		t.Fatalf("LogDays=0 deleted %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(keepDir, "work", "f.bin")); err != nil {
		t.Fatalf("LogDays=0 removed work: %v", err)
	}

	// Next turn on the pruned session can recreate the scratch folder.
	dir, own, err := a.chatTurnDir("chat_idle_w", "")
	if err != nil || own || dir != a.chatScratchDir("chat_idle_w") {
		t.Fatalf("chatTurnDir after prune: dir=%s own=%v err=%v", dir, own, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("scratch folder not recreated: %v", err)
	}
}
