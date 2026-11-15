package optimize

import (
	"context"
	"errors"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/version"
)

func proposalStatus(t *testing.T, s *Service, pid string) string {
	t.Helper()
	var st string
	if err := s.DB.QueryRow(`SELECT status FROM skill_proposals WHERE id=?`, pid).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func pointerAt(t *testing.T, s *Service, skill, name string) string {
	t.Helper()
	p, _ := s.Ver.GetPointer(context.Background(), version.EntitySkill, skill, name)
	return p.String
}

// failStatus makes any status change of skill_proposals fail (a busy or full
// database at the second write).
func failStatus(t *testing.T, s *Service) func() {
	t.Helper()
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_status BEFORE UPDATE OF status ON skill_proposals WHEN NEW.status <> OLD.status BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	return func() { _, _ = s.DB.Exec(`DROP TRIGGER fail_status`) }
}

// Promoting moves current, ends the canary and marks the proposal in one go: a
// failed status write leaves current where it was (before: current moved, the
// proposal stayed "canary" and the canary pointer kept taking runs).
func TestPromoteIsAtomic(t *testing.T) {
	s, skill := setup(t)
	ctx := context.Background()
	v1 := pointerAt(t, s, skill, version.PointerCurrent)
	_ = s.SetSettings(ctx, skill, Settings{Mode: "canary", Ratio: 1, Runs: 4})
	p, err := s.Propose(ctx, skill, edit("3. Verify the archive.", "3. Verify with tar -tf."), "auto", "robustness", "r")
	if err != nil || p.Status != "canary" {
		t.Fatalf("%v %+v", err, p)
	}
	undo := failStatus(t, s)
	if err := s.Promote(ctx, p.ID); err == nil {
		t.Fatal("promote reported success with the status write failing")
	}
	if got := pointerAt(t, s, skill, version.PointerCurrent); got != v1 {
		t.Fatalf("current moved without the proposal: %s", got)
	}
	if got := pointerAt(t, s, skill, version.PointerCanary); got != p.VersionID {
		t.Fatalf("canary cleared without the proposal: %q", got)
	}
	undo()
	if err := s.Promote(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if pointerAt(t, s, skill, version.PointerCurrent) != p.VersionID || pointerAt(t, s, skill, version.PointerCanary) != "" || proposalStatus(t, s, p.ID) != "promoted" {
		t.Fatal("promote incomplete")
	}
	if err := s.Promote(ctx, p.ID); err == nil {
		t.Fatal("promoted twice")
	}
}

// Discarding a canary ends it with the status; a failed write keeps both (a
// "discarded" proposal never keeps getting runs). Rejecting a pending proposal
// leaves another proposal's canary alone.
func TestDiscardIsAtomic(t *testing.T) {
	s, skill := setup(t)
	ctx := context.Background()
	_ = s.SetSettings(ctx, skill, Settings{Mode: "canary", Ratio: 1, Runs: 4})
	c, err := s.Propose(ctx, skill, edit("3. Verify the archive.", "3. Verify with tar -tf."), "auto", "robustness", "r")
	if err != nil || c.Status != "canary" {
		t.Fatalf("%v %+v", err, c)
	}
	pend, err := s.Propose(ctx, skill, edit("5. Report size.", "5. Report the size."), "user", "clarity", "r")
	if err != nil || pend.Status != "pending" {
		t.Fatalf("%v %+v", err, pend)
	}
	if err := s.Discard(ctx, pend.ID); err != nil {
		t.Fatal(err)
	}
	if pointerAt(t, s, skill, version.PointerCanary) != c.VersionID || proposalStatus(t, s, pend.ID) != "rejected" {
		t.Fatal("rejecting a pending proposal touched the running canary")
	}
	undo := failStatus(t, s)
	if err := s.Discard(ctx, c.ID); err == nil {
		t.Fatal("discard reported success with the status write failing")
	}
	if pointerAt(t, s, skill, version.PointerCanary) != c.VersionID || proposalStatus(t, s, c.ID) != "canary" {
		t.Fatal("half discarded")
	}
	undo()
	if err := s.Discard(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if pointerAt(t, s, skill, version.PointerCanary) != "" || proposalStatus(t, s, c.ID) != "discarded" {
		t.Fatal("discard incomplete")
	}
}

// PromoteOver replaces only the versions it was told about.
func TestPromoteOverRefusesAnotherCurrent(t *testing.T) {
	s, skill := setup(t)
	ctx := context.Background()
	v1 := pointerAt(t, s, skill, version.PointerCurrent)
	p, err := s.Propose(ctx, skill, edit("5. Report size.", "5. Report the size."), "session", "session", "r")
	if err != nil || p.Status != "pending" {
		t.Fatalf("%v %+v", err, p)
	}
	// the skill moves on meanwhile (edited elsewhere)
	other, _, err := s.Skills.CommitFiles(ctx, skill, "backup", edit("6. Done.", "6. Finished."), "edit", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PromoteOver(ctx, p.ID, v1); !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
	if pointerAt(t, s, skill, version.PointerCurrent) != other.ID || proposalStatus(t, s, p.ID) != "pending" {
		t.Fatal("a refused promote changed something")
	}
	if err := s.PromoteOver(ctx, p.ID, v1, other.ID); err != nil {
		t.Fatal(err)
	}
	if pointerAt(t, s, skill, version.PointerCurrent) != p.VersionID {
		t.Fatal("not promoted")
	}
}
