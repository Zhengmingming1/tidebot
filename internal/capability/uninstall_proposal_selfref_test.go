package capability

import (
	"context"
	"testing"
	"time"
)

// The owner confirms an uninstall card: the reviewed summary is computed while
// the card is pending, then the card is claimed ('applying') and the summary is
// recomputed under the writer lock. The card's own payload lists the skills it
// removes; it must not count as a live reference to them, or the two summaries
// never match and every confirmed uninstall with an exclusive skill fails.
func TestUninstallProposalDoesNotRetainItsOwnSkills(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	sid := r.Lock.Components[0].SkillID
	insertChatSession(t, s, "chat_live")
	reviewed, err := s.PreviewUninstall(ctx, r.ID, r.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewed.RemovedSkills) != 1 || reviewed.RemovedSkills[0] != sid {
		t.Fatalf("summary should remove the exclusive skill: %+v", reviewed)
	}
	p, err := s.CreateProposal(ctx, Proposal{SessionID: "chat_live", Kind: "uninstall", Lock: r.Lock, BaseVersionID: r.VersionID, UninstallPreview: &reviewed})
	if err != nil {
		t.Fatal(err)
	}
	// A second pending uninstall card (the model proposed twice) is not a reference either.
	if _, err = s.CreateProposal(ctx, Proposal{SessionID: "chat_live", Kind: "uninstall", Lock: r.Lock, BaseVersionID: r.VersionID, UninstallPreview: &reviewed}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PreviewUninstall(ctx, r.ID, r.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if retainedByHistory(pending, sid) || len(pending.RemovedSkills) != 1 {
		t.Fatalf("pending uninstall card retained its own skill: %+v", pending)
	}
	claimed, err := s.ClaimProposal(ctx, p.ID, "chat_live", "ui")
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.UninstallVersionWithReport(ctx, r.ID, r.VersionID, "ui", *claimed.UninstallPreview)
	if err != nil {
		t.Fatalf("confirmed uninstall refused: %v", err)
	}
	if !out.OK || len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != sid {
		t.Fatalf("report: %+v", out)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, sid).Scan(&n); err != nil || n != 0 {
		t.Fatal("exclusive skill registration kept", n, err)
	}
}

// Proposals that would install or pin the skill still retain it, whatever the
// status of an unrelated uninstall card.
func TestUninstallStillRetainsSkillForLiveInstallProposal(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	sid := r.Lock.Components[0].SkillID
	insertChatSession(t, s, "chat_live")
	insertProposal(t, s, "capprop_update", "chat_live", "pending", time.Now().Add(time.Hour), sid)
	out, err := s.PreviewUninstall(ctx, r.ID, r.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if !retainedByHistory(out, sid) || len(out.RemovedSkills) != 0 {
		t.Fatalf("live update proposal must retain the skill: %+v", out)
	}
}
