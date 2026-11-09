package capability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failRemove makes deleting paths whose base name has the given prefix fail
// until allowed, like a file held open by another process on Windows.
func failRemove(t *testing.T, prefix string) (allow func()) {
	t.Helper()
	orig := gcRemoveAll
	t.Cleanup(func() { gcRemoveAll = orig })
	blocked := true
	gcRemoveAll = func(path string) error {
		if blocked && strings.HasPrefix(filepath.Base(path), prefix) {
			return errors.New("file in use")
		}
		return orig(path)
	}
	return func() { blocked = false }
}

// A snapshot whose files cannot be deleted after its row is committed must not
// abort the remaining versions, report an error for work that succeeded, or
// lose the freed bytes; the leftover trash is retried by the next GC.
func TestGCContinuesWhenTrashRemovalFails(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	versions := gcFixture(t, s, 5, 10)
	base := filepath.Join(s.DataDir, "capabilities", "example")
	stuck := ".gc-" + versions[0].VersionID
	// Snapshot sizes differ slightly (each lock carries its own identity).
	sizes := map[string]int64{}
	for _, v := range versions[:3] {
		n, err := gcSnapshotSize(ctx, filepath.Join(base, v.VersionID))
		if err != nil {
			t.Fatal(err)
		}
		sizes[v.VersionID] = n
	}
	allow := failRemove(t, stuck)

	gc, err := s.GC(ctx, "example", "ui")
	if err != nil {
		t.Fatal("GC reported an error after committing:", err)
	}
	if len(gc.RemovedVersions) != 3 {
		t.Fatalf("removed %v, want the 3 old versions", gc.RemovedVersions)
	}
	list, _ := s.Versions(ctx, "example")
	if len(list) != 2 {
		t.Fatalf("versions left = %d", len(list))
	}
	if _, err := os.Stat(filepath.Join(base, stuck)); err != nil {
		t.Fatal("stuck trash should still be on disk:", err)
	}
	for _, v := range versions[1:3] {
		if _, err := os.Stat(filepath.Join(base, ".gc-"+v.VersionID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("trash of a deletable version left behind", v.VersionID)
		}
	}
	if want := sizes[versions[1].VersionID] + sizes[versions[2].VersionID]; gc.BytesFreed != want {
		t.Fatalf("bytes freed %d, want %d (stuck snapshot not counted)", gc.BytesFreed, want)
	}
	perVersion := sizes[versions[0].VersionID]

	allow()
	again, err := s.GC(ctx, "example", "ui")
	if err != nil || len(again.RemovedVersions) != 0 || again.BytesFreed != perVersion {
		t.Fatalf("retry GC = %+v, %v; want %d bytes from the leftover", again, err, perVersion)
	}
	if _, err := os.Stat(filepath.Join(base, stuck)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("leftover trash not retried")
	}
}

// The retry never touches trash whose version row still exists: that is a
// GC in progress (renamed, not yet committed) or a crash for startup recovery.
func TestGCSweepKeepsTrashWithRow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	versions := gcFixture(t, s, 3, 0)
	base := filepath.Join(s.DataDir, "capabilities", "example")
	v := versions[0].VersionID
	if err := s.Bind(ctx, Binding{OwnerKind: "task", OwnerID: "t", CapabilityID: "example", VersionID: v}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(base, v), filepath.Join(base, ".gc-"+v)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(ctx, "example", "ui"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, ".gc-"+v)); err != nil {
		t.Fatal("GC deleted trash of a version that still has a row:", err)
	}
	// Startup recovery puts it back.
	if _, err := s.RecoverStorage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, "example", v); err != nil {
		t.Fatal(err)
	}
}

// Startup recovery keeps going past an entry it cannot delete.
func TestRecoverStorageContinuesPastRemovalFailure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, err := s.Install(ctx, testLock(), testFiles())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(s.DataDir, "capabilities", r.ID)
	var paths []string
	for _, name := range []string{".stage-a", ".stage-b", "capver_orphan", ".gc-capver_gone"} {
		p := filepath.Join(base, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	failRemove(t, ".stage-a")
	n, err := s.RecoverStorage(ctx)
	if err == nil {
		t.Fatal("failure not reported")
	}
	if n != 3 {
		t.Fatalf("recovered %d, want 3", n)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatal("stuck entry should remain", err)
	}
	for _, p := range paths[1:] {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("recovery stopped before", filepath.Base(p))
		}
	}
	if err := s.Verify(ctx, r.ID, r.VersionID); err != nil {
		t.Fatal(err)
	}
}
