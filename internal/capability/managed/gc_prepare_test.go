package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func writeOwnedVersion(t testing.TB, root, kind, name, version string, created time.Time, files int) string {
	t.Helper()
	dir := filepath.Join(root, kind, name, version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := ownership{Format: "tidebot-managed/1", Reference: Reference{Kind: kind, Name: name, Version: version}, Created: created, LastUsed: created, Links: map[string]string{}}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, ownerFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < files; i++ {
		if err = os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// AfterPrepare must run before any tree walk or deletion, so a caller can drop
// a database lock once live paths have been moved aside.
func TestGCAfterPrepareRunsBeforeDiskWork(t *testing.T) {
	root := t.TempDir()
	old := writeOwnedVersion(t, root, "tools", "cli", "1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 20)
	keep := writeOwnedVersion(t, root, "tools", "cli", "2", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 20)
	i := &Installer{DataDir: root}
	var order []string
	t.Cleanup(func() { GCPurgeHook = func() {} })
	GCPurgeHook = func() { order = append(order, "purge") }
	rep, err := i.GC(context.Background(), GCOptions{
		KeepPrevious: 1,
		AfterPrepare: func() error { order = append(order, "prepare"); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "prepare" || order[1] != "purge" {
		t.Fatalf("order %v, want prepare then purge", order)
	}
	if len(rep.Removed) != 1 || rep.Removed[0].Version != "1" || rep.FreedBytes == 0 {
		t.Fatalf("report %+v", rep)
	}
	if _, err = os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old version still in place")
	}
	if _, err = os.Stat(keep); err != nil {
		t.Fatal("kept previous version:", err)
	}
	if _, err = os.Lstat(filepath.Join(root, gcPendingDir)); !os.IsNotExist(err) {
		t.Fatal("pending directory left behind")
	}
}

func TestPurgeGCPendingRemovesLeftoverTrash(t *testing.T) {
	root := t.TempDir()
	leftover := filepath.Join(root, gcPendingDir, "old", "tools", "cli", "1")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "x"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := PurgeGCPending(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("freed %d, want 5", n)
	}
	if _, err = os.Lstat(filepath.Join(root, gcPendingDir)); !os.IsNotExist(err) {
		t.Fatal("pending directory left")
	}
}

func TestGCPurgesLeftoverPendingFromEarlierRun(t *testing.T) {
	root := t.TempDir()
	leftover := filepath.Join(root, gcPendingDir, "old", "tools", "cli", "9")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "x"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOwnedVersion(t, root, "tools", "cli", "1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1)
	i := &Installer{DataDir: root}
	if _, err := i.GC(context.Background(), GCOptions{KeepPrevious: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, gcPendingDir)); !os.IsNotExist(err) {
		t.Fatal("leftover pending survived GC")
	}
}

// A version name may be reused after we move the old tree aside. The new
// directory must stay; only the isolated copy is deleted.
func TestGCDoesNotDeleteReuseAtLivePath(t *testing.T) {
	root := t.TempDir()
	old := writeOwnedVersion(t, root, "tools", "cli", "1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 3)
	writeOwnedVersion(t, root, "tools", "cli", "2", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 1)
	i := &Installer{DataDir: root}
	rep, err := i.GC(context.Background(), GCOptions{
		KeepPrevious: 1,
		AfterPrepare: func() error {
			if err := os.MkdirAll(old, 0o700); err != nil {
				t.Fatal(err)
			}
			return os.WriteFile(filepath.Join(old, "new"), []byte("fresh"), 0o600)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0].Version != "1" {
		t.Fatalf("report %+v", rep)
	}
	b, err := os.ReadFile(filepath.Join(old, "new"))
	if err != nil || string(b) != "fresh" {
		t.Fatalf("live reuse removed: %s %v", b, err)
	}
}

func TestGCRestoresTamperedTreeAndLeavesLinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges often unavailable on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious")
	if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := writeOwnedVersion(t, root, "tools", "cli", "1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1)
	if err := os.Symlink(precious, filepath.Join(old, "link")); err != nil {
		t.Fatal(err)
	}
	writeOwnedVersion(t, root, "tools", "cli", "2", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 1)
	i := &Installer{DataDir: root}
	if _, err := i.GC(context.Background(), GCOptions{KeepPrevious: 1}); err == nil {
		t.Fatal("tampered tree accepted")
	}
	if _, err := os.Stat(filepath.Join(old, "link")); err != nil {
		t.Fatal("tampered tree not moved back:", err)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatal("link target touched:", err)
	}
}
