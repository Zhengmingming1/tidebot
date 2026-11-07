package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func placeChatTranscript(t *testing.T, a *App, csid, folder string, size int, age time.Duration) (jsonl, side string) {
	t.Helper()
	dir := filepath.Join(a.claudeConfigDir(), "projects", folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	jsonl = filepath.Join(dir, csid+".jsonl")
	if err := os.WriteFile(jsonl, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	side = filepath.Join(dir, csid)
	if err := os.MkdirAll(side, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(side, "tool.json"), make([]byte, size/2), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	_ = os.Chtimes(jsonl, ts, ts)
	return jsonl, side
}

func TestChatTranscriptRetentionOrphansAndIdle(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	day := 24 * time.Hour

	orphanID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	idleID := "11111111-2222-4333-8444-555555555555"
	freshID := "66666666-7777-4888-8999-aaaaaaaaaaaa"
	runID := "99999999-aaaa-4bbb-8ccc-dddddddddddd"
	junk := "not-a-uuid"

	oJSONL, oSide := placeChatTranscript(t, a, orphanID, "proj-orphan", 40<<10, 2*day)
	iJSONL, iSide := placeChatTranscript(t, a, idleID, "proj-idle", 80<<10, 2*day)
	fJSONL, fSide := placeChatTranscript(t, a, freshID, "proj-fresh", 20<<10, 2*day)
	rJSONL, rSide := placeChatTranscript(t, a, runID, "proj-run", 30<<10, 40*day)
	junkPath := filepath.Join(a.claudeConfigDir(), "projects", "proj-orphan", junk+".jsonl")
	if err := os.WriteFile(junkPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	idleUpdated := time.Now().UTC().Add(-40 * day).Format(time.RFC3339)
	freshUpdated := time.Now().UTC().Add(-2 * day).Format(time.RFC3339)
	runUpdated := time.Now().UTC().Add(-40 * day).Format(time.RFC3339)
	for _, row := range []struct {
		id, csid, status, updated string
	}{
		{"chat_idle", idleID, "idle", idleUpdated},
		{"chat_fresh", freshID, "idle", freshUpdated},
		{"chat_run", runID, "running", runUpdated},
	} {
		if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, claude_session_id, status, created_at, updated_at) VALUES(?,?,?,?,?)`,
			row.id, row.csid, row.status, row.updated, row.updated); err != nil {
			t.Fatal(err)
		}
	}

	beforeFiles := 0
	beforeBytes := int64(0)
	for _, p := range []string{oJSONL, oSide, iJSONL, iSide, fJSONL, fSide, rJSONL, rSide, junkPath} {
		if fi, err := os.Stat(p); err == nil {
			beforeFiles++
			if fi.IsDir() {
				s, _ := dirSize(p)
				beforeBytes += s
			} else {
				beforeBytes += fi.Size()
			}
		}
	}

	now := time.Now()
	plan, err := a.planRetention(ctx, RetentionPolicy{LogDays: 30, ArtifactDays: 90}, now)
	if err != nil {
		t.Fatal(err)
	}
	wantDel := map[string]bool{orphanID: true, idleID: true}
	gotDel := map[string]bool{}
	var planned int64
	for _, f := range plan.del {
		if f.kind != "chat" {
			continue
		}
		gotDel[f.runID] = true
		planned += f.size
	}
	for id := range wantDel {
		if !gotDel[id] {
			t.Fatalf("planned del missing %s: %+v", id, plan.del)
		}
	}
	for id := range gotDel {
		if !wantDel[id] {
			t.Fatalf("unexpected planned del %s", id)
		}
	}
	if planned < (40<<10)+(20<<10)+(80<<10)+(40<<10) { // orphan+side + idle+side (approx)
		t.Fatalf("planned free too small: %d", planned)
	}

	rep, err := a.ApplyRetention(ctx, RetentionPolicy{LogDays: 30, ArtifactDays: 90}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedFiles != 2 {
		t.Fatalf("deleted_files=%d freed=%d", rep.DeletedFiles, rep.FreedBytes)
	}
	if rep.FreedBytes < planned {
		t.Fatalf("freed %d < planned %d", rep.FreedBytes, planned)
	}
	gone := func(p string) bool { _, err := os.Stat(p); return os.IsNotExist(err) }
	if !gone(oJSONL) || !gone(oSide) || !gone(iJSONL) || !gone(iSide) {
		t.Fatal("orphan/idle transcript kept")
	}
	if gone(fJSONL) || gone(fSide) || gone(rJSONL) || gone(rSide) || gone(junkPath) {
		t.Fatal("fresh/running/junk removed")
	}
	// Empty project folder for idle should be removed; orphan folder still has junk.
	if !gone(filepath.Join(a.claudeConfigDir(), "projects", "proj-idle")) {
		t.Fatal("empty project folder kept")
	}
	if gone(filepath.Join(a.claudeConfigDir(), "projects", "proj-orphan")) {
		t.Fatal("project folder with leftover removed")
	}

	// LogDays=0 keeps idle transcripts; orphans still go (re-seed fixture).
	placeChatTranscript(t, a, orphanID, "proj-orphan2", 8<<10, day)
	idle2 := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	placeChatTranscript(t, a, idle2, "proj-idle2", 8<<10, day)
	old := time.Now().UTC().Add(-90 * day).Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, claude_session_id, status, created_at, updated_at) VALUES(?,?,?,?,?)`,
		"chat_idle2", idle2, "idle", old, old); err != nil {
		t.Fatal(err)
	}
	rep, err = a.ApplyRetention(ctx, RetentionPolicy{LogDays: 0, ArtifactDays: 0}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DeletedFiles != 1 {
		t.Fatalf("LogDays=0 should only drop orphans, got %+v", rep)
	}
	if len(a.chatTranscripts(idle2)) != 1 {
		t.Fatal("idle transcript removed despite LogDays=0")
	}
	if len(a.chatTranscripts(orphanID)) != 0 {
		t.Fatal("orphan kept despite LogDays=0")
	}

	_ = beforeFiles
	_ = beforeBytes
}

func TestChatTranscriptRetentionPreviewCounts(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	day := 24 * time.Hour
	csid := "cccccccc-dddd-4eee-8fff-000000000001"
	placeChatTranscript(t, a, csid, "proj-prev", 16<<10, 2*day)
	old := time.Now().UTC().Add(-40 * day).Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO chat_sessions(id, claude_session_id, status, created_at, updated_at) VALUES(?,?,?,?,?)`,
		"chat_prev", csid, "idle", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"keep_per_task": 0}); err != nil {
		t.Fatal(err)
	}
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Preview.Files < 1 || v.Preview.Bytes < 16<<10 {
		t.Fatalf("preview missing chat transcript: %+v", v.Preview)
	}
}
