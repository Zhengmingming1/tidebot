package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

func insertTestRun(t *testing.T, a *App, id string) {
	t.Helper()
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES(?,'running',?)`, id, nowUTC()); err != nil {
		t.Fatal(err)
	}
}

func stubClaudeVersion(t *testing.T, v string) {
	t.Helper()
	claudeVer.Lock()
	oldV, oldAt := claudeVer.v, claudeVer.at
	claudeVer.v = v
	claudeVer.at = time.Now()
	claudeVer.Unlock()
	t.Cleanup(func() {
		claudeVer.Lock()
		claudeVer.v, claudeVer.at = oldV, oldAt
		claudeVer.Unlock()
	})
}

func blockSQL(t *testing.T, a *App, name, when string) {
	t.Helper()
	table := "test_block_" + name
	if _, err := a.DB.Exec(`CREATE TABLE ` + table + `(x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO ` + table + ` VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`CREATE TRIGGER test_` + name + `_busy ` + when + `
AND EXISTS (SELECT 1 FROM ` + table + `)
BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = a.DB.Exec(`DROP TRIGGER IF EXISTS test_` + name + `_busy`)
		_, _ = a.DB.Exec(`DROP TABLE IF EXISTS ` + table)
	})
}

func unblockSQL(t *testing.T, a *App, name string) {
	t.Helper()
	if _, err := a.DB.Exec(`DELETE FROM test_block_` + name); err != nil {
		t.Fatal(err)
	}
}

func agentConfig(path string) []guard.ConfigChange {
	return []guard.ConfigChange{{Path: path, Change: "modified"}}
}

// A canceled caller context (user stop, quit) used to abort the write at once
// and drop the error, so the run had no Claude Code version. The write must
// still land, and a busy first try must be retried.
func TestRecordRunMetaRetriesAndIgnoresCanceledContext(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	insertTestRun(t, a, "rm1")
	stubClaudeVersion(t, "2.1.3")
	blockSQL(t, a, "meta", `BEFORE INSERT ON run_flags WHEN 1`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		a.recordRunMeta(ctx, "rm1")
		done <- nil
	}()

	waitFor(t, 2*time.Second, func() bool {
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM run_flags WHERE run_id='rm1'`).Scan(&n)
		return n == 0
	})
	time.Sleep(30 * time.Millisecond)
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM run_flags WHERE run_id='rm1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("wrote before unlock: n=%d err=%v", n, err)
	}

	unblockSQL(t, a, "meta")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not finish")
	}
	var ver string
	if err := a.DB.QueryRow(`SELECT claude_version FROM run_flags WHERE run_id='rm1'`).Scan(&ver); err != nil || ver != "2.1.3" {
		t.Fatalf("claude_version %q %v", ver, err)
	}
}

// The after-run check used to try once for 5s, log the error, and still notify,
// so the owner opened a run page with no file list. The write is retried, the
// notice waits until it lands, and giving up does not notify.
func TestRecordAgentConfigChangesRetriesWhenDatabaseBusy(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	insertTestRun(t, a, "ac1")
	path := filepath.Join(t.TempDir(), ".mcp.json")
	blockSQL(t, a, "acc", `BEFORE UPDATE ON runs WHEN NEW.agent_config_changes<>''`)

	ch := a.Events.Subscribe(16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.recordAgentConfigChanges(context.Background(), "ac1", "", agentConfig(path))
	}()

	time.Sleep(40 * time.Millisecond)
	select {
	case e := <-ch:
		t.Fatalf("notified before save: %+v", e)
	default:
	}
	var raw string
	_ = a.DB.QueryRow(`SELECT agent_config_changes FROM runs WHERE id='ac1'`).Scan(&raw)
	if raw != "" {
		t.Fatalf("saved before unlock: %q", raw)
	}

	unblockSQL(t, a, "acc")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not finish")
	}
	if err := a.DB.QueryRow(`SELECT agent_config_changes FROM runs WHERE id='ac1'`).Scan(&raw); err != nil || raw == "" {
		t.Fatalf("changes %q %v", raw, err)
	}
	var logs int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM operation_log WHERE entity_id='ac1' AND action='agent_config_changed'`).Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("operation_log %d %v", logs, err)
	}
	select {
	case e := <-ch:
		if e.Kind != EventAgentConfigChanged || e.RunID != "ac1" || e.Detail == "" {
			t.Fatalf("%+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no notice after save")
	}
}

func TestRecordAgentConfigChangesNoNotifyIfWriteFails(t *testing.T) {
	wait, pause, total := writeRetryWait, writeRetryPause, writeRetryFor
	t.Cleanup(func() { writeRetryWait, writeRetryPause, writeRetryFor = wait, pause, total })
	writeRetryWait, writeRetryPause, writeRetryFor = 20*time.Millisecond, time.Millisecond, 40*time.Millisecond

	a := newTestApp(t)
	insertTestRun(t, a, "ac2")
	blockSQL(t, a, "accfail", `BEFORE UPDATE ON runs WHEN NEW.agent_config_changes<>''`)

	ch := a.Events.Subscribe(16)
	a.recordAgentConfigChanges(context.Background(), "ac2", "", agentConfig("/tmp/.mcp.json"))

	var raw string
	_ = a.DB.QueryRow(`SELECT agent_config_changes FROM runs WHERE id='ac2'`).Scan(&raw)
	if raw != "" {
		t.Fatalf("saved after giving up: %q", raw)
	}
	var logs int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM operation_log WHERE entity_id='ac2' AND action='agent_config_changed'`).Scan(&logs)
	if logs != 0 {
		t.Fatalf("operation_log %d", logs)
	}
	select {
	case e := <-ch:
		t.Fatalf("notified after failed save: %+v", e)
	default:
	}
}

// Both writes are one transaction and safe to repeat: a second call after a
// successful save must not add another operation_log row.
func TestRecordAgentConfigChangesIdempotentLog(t *testing.T) {
	a := newTestApp(t)
	insertTestRun(t, a, "ac3")
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	a.recordAgentConfigChanges(context.Background(), "ac3", "", agentConfig(path))
	a.recordAgentConfigChanges(context.Background(), "ac3", "", agentConfig(path))
	var logs int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM operation_log WHERE entity_id='ac3' AND action='agent_config_changed'`).Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("operation_log %d %v", logs, err)
	}
}
