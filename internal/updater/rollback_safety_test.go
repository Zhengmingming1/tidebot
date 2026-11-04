package updater

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func rollbackFixture(t *testing.T) Swap {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agentd")
	for name, data := range map[string]string{p: "new", p + ".prev": "old"} {
		if err := os.WriteFile(name, []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return Swap{Path: p, Prev: p + ".prev"}
}
func assertBytes(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != want {
		t.Fatalf("%s = %q, %v; want %q", path, b, err, want)
	}
}
func TestRollbackMissingBackupPreservesExecutable(t *testing.T) {
	s := rollbackFixture(t)
	if err := os.Remove(s.Prev); err != nil {
		t.Fatal(err)
	}
	if err := rollbackSwaps([]Swap{s}); err == nil {
		t.Fatal("expected missing-backup error")
	}
	assertBytes(t, s.Path, "new")
}
func TestRollbackPreservesExistingRecoveryCopy(t *testing.T) {
	s := rollbackFixture(t)
	if err := os.WriteFile(s.Path+".failed", []byte("recovery"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := rollbackSwaps([]Swap{s}); err == nil {
		t.Fatal("expected recovery-path conflict")
	}
	assertBytes(t, s.Path, "new")
	assertBytes(t, s.Prev, "old")
	assertBytes(t, s.Path+".failed", "recovery")
}
func TestRollbackRestoreFailurePreservesExecutable(t *testing.T) {
	for _, failRecovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover current", true: "retain recovery copy"}[failRecovery], func(t *testing.T) {
			s := rollbackFixture(t)
			injected := errors.New("injected rename failure")
			err := rollbackSwapWithFileOps(s, func(from, to string) error {
				if from == s.Prev || (failRecovery && from == s.Path+".failed") {
					return injected
				}
				return os.Rename(from, to)
			}, os.RemoveAll)
			if !errors.Is(err, injected) {
				t.Fatalf("expected restore failure, got %v", err)
			}
			if failRecovery {
				assertBytes(t, s.Path+".failed", "new")
			} else {
				assertBytes(t, s.Path, "new")
			}
			assertBytes(t, s.Prev, "old")
		})
	}
}
func TestRollbackContinuesAfterFailure(t *testing.T) {
	good, bad := rollbackFixture(t), rollbackFixture(t)
	if err := os.Remove(bad.Prev); err != nil {
		t.Fatal(err)
	}
	if err := rollbackSwaps([]Swap{good, bad}); err == nil {
		t.Fatal("expected rollback error")
	}
	assertBytes(t, good.Path, "old")
	assertBytes(t, bad.Path, "new")
}
func TestRollbackMissingCurrentRestoresBackup(t *testing.T) {
	s := rollbackFixture(t)
	if err := os.Remove(s.Path); err != nil {
		t.Fatal(err)
	}
	if err := rollbackSwaps([]Swap{s}); err != nil {
		t.Fatal(err)
	}
	assertBytes(t, s.Path, "old")
}
func TestRollbackNowMissingBackupReportsFailure(t *testing.T) {
	s := rollbackFixture(t)
	if err := os.Remove(s.Prev); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := writeJSON(pendingPath(state), Pending{Swaps: []Swap{s}}); err != nil {
		t.Fatal(err)
	}
	if err := RollbackNow(state, "unhealthy"); err == nil {
		t.Fatal("expected rollback error")
	}
	assertBytes(t, s.Path, "new")
	if r := LastResult(state); r == nil || r.Status != "failed" {
		t.Fatalf("result = %+v", r)
	}
}

func TestBootMissingBackupReportsFailure(t *testing.T) {
	s := rollbackFixture(t)
	if err := os.Remove(s.Prev); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := writeJSON(pendingPath(state), Pending{Swaps: []Swap{s}, Attempts: MaxAttempts}); err != nil {
		t.Fatal(err)
	}
	if bs := Boot(state); bs.RolledBack {
		t.Fatal("failed restore reported as rolled back")
	}
	assertBytes(t, s.Path, "new")
	if r := LastResult(state); r == nil || r.Status != "failed" {
		t.Fatalf("result = %+v", r)
	}
}

func TestRollbackStagingFailurePreservesBothExecutables(t *testing.T) {
	s := rollbackFixture(t)
	calls := 0
	err := rollbackSwapWithFileOps(s, func(from, to string) error {
		calls++
		return os.ErrPermission
	}, os.RemoveAll)
	if !errors.Is(err, os.ErrPermission) || calls != 1 {
		t.Fatalf("err = %v, rename calls = %d", err, calls)
	}
	assertBytes(t, s.Path, "new")
	assertBytes(t, s.Prev, "old")
}

func TestRollbackCleanupFailureStillReportsSuccess(t *testing.T) {
	s := rollbackFixture(t)
	calls := 0
	err := rollbackSwapWithFileOps(s, os.Rename, func(path string) error {
		calls++
		if path != s.Path+".failed" {
			t.Fatalf("unexpected cleanup path: %s", path)
		}
		assertBytes(t, s.Path, "old")
		return os.ErrPermission // A running Windows executable cannot be deleted.
	})
	if err != nil || calls != 1 {
		t.Fatalf("successful rollback reported err = %v, cleanup calls = %d", err, calls)
	}
	assertBytes(t, s.Path, "old")
	assertBytes(t, s.Path+".failed", "new")
}
