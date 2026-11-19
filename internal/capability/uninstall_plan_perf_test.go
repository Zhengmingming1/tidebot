package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func sameSkillRefs(a, b map[string]map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id, versions := range a {
		other, ok := b[id]
		if !ok || len(other) != len(versions) {
			return false
		}
		for vid := range versions {
			if !other[vid] {
				return false
			}
		}
	}
	return true
}

func sameShared(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func seedBulkyOtherLocks(t testing.TB, s *Store, n, noteBytes int) {
	t.Helper()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	note := strings.Repeat("n", noteBytes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("other-cap-%d", i)
		ver := fmt.Sprintf("other-ver-%d", i)
		raw, err := json.Marshal(Lock{
			Format: LockFormat, ID: id, Title: "Other",
			Source:     Source{Kind: "git", Commit: strings.Repeat("a", 40), Trust: "official", Adapter: "skill-dir"},
			Components: []Component{{Type: "skill", Name: "other", SkillID: "sk_other_" + id, VersionID: "ver_x"}},
			Requires:   Requirements{Tools: []ToolRequirement{{Name: "node", Version: "20", Recipes: []Recipe{{Backend: "npm", Package: "x", Docs: note}}}}},
			Scan:       Scan{Scanner: "t", Verdict: "pass", Findings: []Finding{{ID: "f", File: "SKILL.md", Line: 1, Note: note, Severity: "info"}}},
		})
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO capabilities(id,title,status,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, id, "installed", ver, now, now); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES(?,?,?,?,?,?)`, ver, id, string(raw), digest(raw), "{}", now); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Old cleanup hashed and fully decoded every capability's lock. The new path
// only verifies this capability and lets SQLite return the other packages'
// component skill ids, without shipping their scan/recipe blobs into Go.
func TestPlanSkillCleanupLockScanPerf(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	const n, size = 80, 32 * 1024
	seedBulkyOtherLocks(t, s, n, size)

	var bytes int
	if err := s.DB.QueryRow(`SELECT coalesce(sum(length(lock_json)),0) FROM capability_versions`).Scan(&bytes); err != nil {
		t.Fatal(err)
	}

	collectOld := func() (map[string]map[string]bool, map[string]bool) {
		t.Helper()
		rows, err := s.DB.Query(`SELECT capability_id,lock_json,lock_sha256 FROM capability_versions`)
		if err != nil {
			t.Fatal(err)
		}
		own := map[string]map[string]bool{}
		shared := map[string]bool{}
		for rows.Next() {
			var cid, raw, digest string
			if err = rows.Scan(&cid, &raw, &digest); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte(raw))
			if hex.EncodeToString(sum[:]) != digest {
				rows.Close()
				t.Fatal("unexpected digest mismatch")
			}
			var l Lock
			if err = json.Unmarshal([]byte(raw), &l); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for _, c := range l.Components {
				if c.SkillID == "" {
					continue
				}
				if cid != r.ID {
					shared[c.SkillID] = true
					continue
				}
				if own[c.SkillID] == nil {
					own[c.SkillID] = map[string]bool{}
				}
				own[c.SkillID][c.VersionID] = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		return own, shared
	}
	collectNew := func() (map[string]map[string]bool, map[string]bool) {
		t.Helper()
		tx, err := s.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		own, shared, err := loadSkillRefs(ctx, tx, r.ID)
		tx.Rollback()
		if err != nil {
			t.Fatal(err)
		}
		return own, shared
	}

	oldOwn, oldShared := collectOld()
	newOwn, newShared := collectNew()
	if !sameSkillRefs(oldOwn, newOwn) || !sameShared(oldShared, newShared) {
		t.Fatalf("skill refs differ: old own=%v shared=%v; new own=%v shared=%v", oldOwn, oldShared, newOwn, newShared)
	}
	if len(oldOwn) != 1 || len(oldShared) != n {
		t.Fatalf("want 1 owned skill and %d shared, got own=%d shared=%d", n, len(oldOwn), len(oldShared))
	}

	bestOld, bestNew := time.Duration(1<<63-1), time.Duration(1<<63-1)
	rounds := 3
	if os.Getenv("TIDEBOT_PERF") != "" {
		rounds = 7
	}
	for i := 0; i < rounds; i++ {
		start := time.Now()
		collectOld()
		if d := time.Since(start); d < bestOld {
			bestOld = d
		}
		start = time.Now()
		collectNew()
		if d := time.Since(start); d < bestNew {
			bestNew = d
		}
	}
	allocsOld := testing.AllocsPerRun(3, func() { collectOld() })
	allocsNew := testing.AllocsPerRun(3, func() { collectNew() })
	t.Logf("%d other locks, %d KB total: hash+full Lock %s (%.0f allocs); this-cap digest + json_each skill ids %s (%.0f allocs)", n, bytes/1024, bestOld, allocsOld, bestNew, allocsNew)

	start := time.Now()
	if _, err := s.PreviewUninstall(ctx, r.ID, r.VersionID); err != nil {
		t.Fatal(err)
	}
	t.Logf("preview uninstall (includes history scan, writer lock): %s", time.Since(start))
}
