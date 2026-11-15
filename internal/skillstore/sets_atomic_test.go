package skillstore

import (
	"context"
	"errors"
	"testing"
)

func countSets(t *testing.T, s *Service, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A skill-set row never exists without a current version: before, a failed first
// commit left a row (the follow-up delete is best-effort), and ListSets then
// failed for every set.
func TestSaveSetCreateIsAtomic(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	members := setMembers(t, s)
	if _, err := s.DB.Exec(`CREATE TRIGGER no_ptr BEFORE INSERT ON pointers BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	// The old path inserted the row first and tried to delete it after a failed
	// commit; if that delete also fails, the half-created set remains.
	if _, err := s.DB.Exec(`CREATE TRIGGER no_cleanup BEFORE DELETE ON skill_sets BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSet(ctx, "", "Research", "", members); err == nil {
		t.Fatal("create succeeded with pointer writes failing")
	}
	if n := countSets(t, s, `SELECT COUNT(*) FROM skill_sets`); n != 0 {
		t.Fatalf("skill sets left behind: %d", n)
	}
	if n := countSets(t, s, `SELECT COUNT(*) FROM versions WHERE entity_type=?`, SkillSetEntity); n != 0 {
		t.Fatalf("skill-set versions left behind: %d", n)
	}
	if _, err := s.ListSets(ctx); err != nil {
		t.Fatalf("list after failed create: %v", err)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER no_ptr`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER no_cleanup`); err != nil {
		t.Fatal(err)
	}
	set, err := s.SaveSet(ctx, "", "Research", "", members)
	if err != nil || set.VersionNo != 1 || set.Name != "Research" {
		t.Fatalf("%+v %v", set, err)
	}
}

// A failed save changes neither the version nor the row, and saving again with
// the same expected version is not refused as a conflict.
func TestSaveSetUpdateIsAtomic(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	members := setMembers(t, s)
	set, err := s.SaveSet(ctx, "", "Research", "", members)
	if err != nil {
		t.Fatal(err)
	}
	v1 := set.VersionID
	if _, err := s.DB.Exec(`CREATE TRIGGER no_sync BEFORE UPDATE OF name ON skill_sets BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSet(ctx, set.ID, "Changed", v1, members); err == nil {
		t.Fatal("save succeeded with the row sync failing")
	}
	got, err := s.GetSet(ctx, set.ID)
	if err != nil || got.VersionID != v1 || got.Name != "Research" {
		t.Fatalf("half saved: version %s name %q err %v", got.VersionID, got.Name, err)
	}
	var rowName string
	if err := s.DB.QueryRow(`SELECT name FROM skill_sets WHERE id=?`, set.ID).Scan(&rowName); err != nil || rowName != "Research" {
		t.Fatalf("row name %q %v", rowName, err)
	}
	if n := countSets(t, s, `SELECT COUNT(*) FROM versions WHERE entity_type=? AND entity_id=?`, SkillSetEntity, set.ID); n != 1 {
		t.Fatalf("versions after failed save: %d", n)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER no_sync`); err != nil {
		t.Fatal(err)
	}
	v2, err := s.SaveSet(ctx, set.ID, "Changed", v1, members)
	if errors.Is(err, ErrSetConflict) || err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ = s.GetSet(ctx, set.ID)
	if got.VersionID != v2.VersionID || got.Name != "Changed" || got.VersionNo != 2 {
		t.Fatalf("after retry: %+v", got)
	}
}
