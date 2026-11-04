package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/managed"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func storageOwnedFixture(t *testing.T, a *App, version string) string {
	t.Helper()
	dir := filepath.Join(a.Cfg.DataDir, "tools", "test-cli", version)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"format": "tidebot-managed/1", "kind": "tools", "name": "test-cli", "version": version, "created": time.Date(2026, 1, int(version[0]-'0'), 0, 0, 0, 0, time.UTC)}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, ".tidebot-managed.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test-cli"), []byte("fixture executable"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func storageLockFixture(t *testing.T, a *App, id, dir string) {
	t.Helper()
	lock := capability.Lock{Format: capability.LockFormat, ID: "storage-cap", Resolved: map[string]capability.Resolved{"test-cli": {Source: "managed", Directory: dir, Path: filepath.Join(dir, "test-cli"), Version: filepath.Base(dir)}}}
	raw, _ := json.Marshal(lock)
	sum := sha256.Sum256(raw)
	_, err := a.DB.Exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES(?,'storage-cap',?,?,'{}',?)`, id, string(raw), hex.EncodeToString(sum[:]), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
}
func storageCapabilityFixture(t *testing.T, a *App) {
	t.Helper()
	_, err := a.DB.Exec(`INSERT INTO capabilities(id,title,status,current_version_id,created_at,updated_at) VALUES('storage-cap','Fixture','uninstalled','version-3','now','now')`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityStorageGCRetainsAllLocksAndRollback(t *testing.T) {
	a := newTestApp(t)
	storageCapabilityFixture(t, a)
	for _, v := range []string{"1", "2", "3", "4", "5"} {
		dir := storageOwnedFixture(t, a, v)
		if v <= "3" {
			storageLockFixture(t, a, "version-"+v, dir)
		}
	}
	// A running pin and a historical pin must both retain their dependencies,
	// even though this capability is uninstalled and neither is current.
	for index, status := range []string{"running", "succeeded"} {
		runID := "storage-run-" + string(rune('1'+index))
		if _, err := a.DB.Exec(`INSERT INTO runs(id,status,created_at) VALUES(?,?,?)`, runID, status, nowUTC()); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Exec(`INSERT INTO capability_run_bindings(run_id,capability_id,version_id) VALUES(?,'storage-cap',?)`, runID, "version-"+string(rune('1'+index))); err != nil {
			t.Fatal(err)
		}
	}
	unowned := filepath.Join(a.Cfg.DataDir, "tools", "test-cli", "0")
	os.MkdirAll(unowned, 0700)
	os.WriteFile(filepath.Join(unowned, "user-file"), []byte("user owned"), 0600)
	cache := filepath.Join(a.Cfg.DataDir, "cache", "npm")
	os.MkdirAll(cache, 0700)
	os.WriteFile(filepath.Join(cache, ".tidebot-cache.json"), []byte(`{"format":"tidebot-cache/1","kind":"npm"}`), 0600)
	old := filepath.Join(cache, "old")
	recent := filepath.Join(cache, "recent")
	os.WriteFile(old, []byte("old cache"), 0600)
	os.WriteFile(recent, []byte("new cache"), 0600)
	before := time.Now().Add(-31 * 24 * time.Hour)
	os.Chtimes(old, before, before)
	ctx := context.Background()
	if _, err := a.capabilityStorageRequest(ctx, "storage_gc", webui.CapabilityRequest{}); err == nil {
		t.Fatal("cleanup lacked explicit confirmation")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("unconfirmed cleanup changed files")
	}
	out, err := a.capabilityStorageRequest(ctx, "storage_gc", webui.CapabilityRequest{Reviewed: true})
	if err != nil {
		t.Fatal(err)
	}
	report := out.(managed.GCReport)
	if len(report.Removed) != 1 || report.Removed[0].Version != "4" || len(report.CacheFiles) != 1 {
		t.Fatalf("unexpected cleanup %+v", report)
	}
	for _, v := range []string{"0", "1", "2", "3", "5"} {
		if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "tools", "test-cli", v)); err != nil {
			t.Fatalf("retained %s: %v", v, err)
		}
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old owned cache retained")
	}
	view, err := a.capabilityStorageRequest(ctx, "storage", webui.CapabilityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	usage := view.(capabilityStorageView)
	if len(usage.Tools) != 3 || usage.Usage.ToolsAndRuntimes == 0 || usage.Limits["cache"] != managed.DefaultCacheQuota {
		t.Fatalf("bad usage %+v", usage)
	}
}
func TestCapabilityStorageGCRefusesApplyingInstall(t *testing.T) {
	a, _, sid := capFixture(t)
	out, err := a.CapabilityRequest(context.Background(), "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	proposal := out.(capabilityProposalView)
	if _, err = a.caps().ClaimProposal(context.Background(), proposal.ID, sid, "ui"); err != nil {
		t.Fatal(err)
	}
	storageOwnedFixture(t, a, "1")
	storageOwnedFixture(t, a, "2")
	_, err = a.capabilityStorageRequest(context.Background(), "storage_gc", webui.CapabilityRequest{Reviewed: true})
	if err == nil || !strings.Contains(err.Error(), "installation") {
		t.Fatalf("expected installation blocker: %v", err)
	}
	if _, err = os.Stat(filepath.Join(a.Cfg.DataDir, "tools", "test-cli", "1")); err != nil {
		t.Fatal("in-flight dependency removed")
	}
}
func TestCapabilityStorageGCRejectsUnresolvableManagedLock(t *testing.T) {
	a := newTestApp(t)
	storageCapabilityFixture(t, a)
	storageOwnedFixture(t, a, "1")
	storageOwnedFixture(t, a, "2")
	storageLockFixture(t, a, "bad-version", t.TempDir())
	_, err := a.capabilityStorageRequest(context.Background(), "storage_gc", webui.CapabilityRequest{Reviewed: true})
	if err == nil {
		t.Fatal("unsafe managed reference accepted")
	}
	if _, err = os.Stat(filepath.Join(a.Cfg.DataDir, "tools", "test-cli", "1")); err != nil {
		t.Fatal("failed reference resolution deleted data")
	}
}
func TestCapabilityManagedReferenceLegacyAndEscapes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "envs", "cap", "proposal-1", "bin", "cli")
	ref, _, err := capabilityManagedReference(root, capability.Resolved{Source: "managed", Path: path})
	if err != nil || ref.Kind != "envs" || ref.Version != "proposal-1" {
		t.Fatalf("legacy path: %+v %v", ref, err)
	}
	for _, r := range []capability.Resolved{
		{Directory: filepath.Join(root, "tools"), Path: path},
		{Directory: filepath.Join(root, "tools", "cli", "1"), Path: filepath.Join(root, "outside")},
		{Path: filepath.Join(root, "cache", "npm", "thing")},
		{Path: "relative/cli"},
	} {
		if _, _, err := capabilityManagedReference(root, r); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}
