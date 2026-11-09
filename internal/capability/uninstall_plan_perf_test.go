package capability

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// seedTaskHistory adds n immutable task versions that never mention the skill,
// so every uninstall plan has to scan all of them.
func seedTaskHistory(t testing.TB, s *Store, n, size int) {
	t.Helper()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", size)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		raw := fmt.Sprintf(`{"prompt":%q,"skills":["sk_other_%d"]}`, body, i)
		if _, err = tx.Exec(`INSERT INTO versions(id,entity_type,entity_id,version_no,content_hash,content_json,source,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("ver_hist_%d", i), "task", fmt.Sprintf("task_hist_%d", i), 1, "h", raw, "test", now); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TIDEBOT_PERF=1 times a reviewed (session card) uninstall on a large history.
func TestUninstallPlanPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	ctx := context.Background()
	for round := 0; round < 3; round++ {
		s := testStore(t)
		r, _ := installOwnedSkill(t, s)
		seedTaskHistory(t, s, 5000, 8192)
		reviewed, err := s.PreviewUninstall(ctx, r.ID, r.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		out, err := s.UninstallVersionWithReport(ctx, r.ID, r.VersionID, "ui", reviewed)
		el := time.Since(start)
		if err != nil || len(out.RemovedSkills) != 1 {
			t.Fatal(out, err)
		}
		t.Logf("round %d: reviewed uninstall %v", round, el)
	}
}

// A confirmed card used to compute the cleanup plan twice under the writer lock
// (once to compare with the reviewed summary, once to delete), scanning every
// task/workflow/session/proposal blob both times.
func TestReviewedUninstallScansHistoryOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	sid := r.Lock.Components[0].SkillID
	reviewed, err := s.PreviewUninstall(ctx, r.ID, r.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	scans := 0
	historyScanHook = func() { scans++ }
	defer func() { historyScanHook = nil }()
	out, err := s.UninstallVersionWithReport(ctx, r.ID, r.VersionID, "ui", reviewed)
	if err != nil {
		t.Fatal(err)
	}
	if scans != 1 {
		t.Fatalf("history scanned %d times, want 1", scans)
	}
	if !out.OK || len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != sid || len(out.Retained) != len(reviewed.Retained) {
		t.Fatalf("report differs from the reviewed plan: %+v vs %+v", out, reviewed)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, sid).Scan(&n); err != nil || n != 0 {
		t.Fatal("skill registration kept", n, err)
	}
}

// A stale reviewed summary still refuses and deletes nothing.
func TestReviewedUninstallMismatchDeletesNothing(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	sid := r.Lock.Components[0].SkillID
	stale := UninstallResult{RemovedSkills: []string{}, Retained: []UninstallRetention{}}
	if _, err := s.UninstallVersionWithReport(ctx, r.ID, r.VersionID, "ui", stale); err == nil || !strings.Contains(err.Error(), "uninstall contents changed") {
		t.Fatalf("stale summary accepted: %v", err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatal("skill removed despite refusal", n, err)
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM capabilities WHERE id=?`, r.ID).Scan(&status); err != nil || status != "installed" {
		t.Fatal("capability changed despite refusal", status, err)
	}
}
