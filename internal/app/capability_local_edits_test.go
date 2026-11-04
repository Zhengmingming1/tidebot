package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func localEditFixture(t *testing.T, edit bool) (*App, *fakeCapabilityGit, string, capability.Record, string) {
	t.Helper()
	a, g, sid := capFixture(t)
	ctx := context.Background()
	v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	p := v.(capabilityProposalView)
	v, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	rec := v.(capability.Record)
	localVersion := rec.Lock.Components[0].VersionID
	if edit {
		localVersion = editCapabilitySkill(t, a, rec, "Local instructions.")
	}
	return a, g, sid, rec, localVersion
}
func editCapabilitySkill(t *testing.T, a *App, rec capability.Record, text string) string {
	t.Helper()
	ctx := context.Background()
	sid := rec.Lock.Components[0].SkillID
	var name string
	if err := a.DB.QueryRow(`SELECT name FROM skills WHERE id=?`, sid).Scan(&name); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"SKILL.md": []byte("---\nname: pdf\ndescription: Local document skill\n---\n" + text + "\n")}
	v, _, err := a.Skills.CommitFiles(ctx, sid, name, files, "user_edit", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}
func proposeLocalUpdate(t *testing.T, a *App, g *fakeCapabilityGit, sid string, rec capability.Record) capability.Proposal {
	t.Helper()
	ctx := context.Background()
	g.commit = strings.Repeat("b", 40)
	g.files = map[string][]byte{"SKILL.md": []byte("---\nname: pdf\ndescription: Updated document skill\n---\nUpstream instructions.\n")}
	value, err := a.CapabilityRequest(ctx, "update", webui.CapabilityRequest{CapabilityID: rec.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	var p capability.Proposal
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestCapabilityUpdateKeepsSkillIdentityAndHistory(t *testing.T) {
	a, g, sid, old, _ := localEditFixture(t, false)
	ctx := context.Background()
	p := proposeLocalUpdate(t, a, g, sid, old)
	value, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	next := value.(capability.Record)
	before, after := old.Lock.Components[0], next.Lock.Components[0]
	if before.SkillID != after.SkillID || before.VersionID == after.VersionID {
		t.Fatal("identity changed or upstream not adopted", before, after)
	}
	pointer, err := a.Ver.GetPointer(ctx, version.EntitySkill, before.SkillID, version.PointerCurrent)
	if err != nil || pointer.String != after.VersionID {
		t.Fatal(pointer, err)
	}
	if err = a.caps().Verify(ctx, old.ID, old.VersionID); err != nil {
		t.Fatal("old snapshot altered", err)
	}
	if _, err = a.Skills.Files(ctx, before.VersionID); err != nil {
		t.Fatal("old skill history removed", err)
	}
}
func TestCapabilityLocalChoicesRequireReviewAndDoNotConsumeCard(t *testing.T) {
	a, g, sid, old, _ := localEditFixture(t, true)
	ctx := context.Background()
	p := proposeLocalUpdate(t, a, g, sid, old)
	if len(p.SkillReconciliation) != 1 || !p.SkillReconciliation[0].LocallyModified {
		t.Fatal(p.SkillReconciliation)
	}
	rec, err := a.caps().Get(ctx, old.ID)
	if err != nil || !rec.LocallyModified {
		t.Fatal(rec, err)
	}
	rows, err := a.caps().List(ctx)
	if err != nil || len(rows) != 1 || !rows[0].LocallyModified {
		t.Fatal(rows, err)
	}
	for _, req := range []webui.CapabilityRequest{
		{Reviewed: true},
		{LocalChoices: map[string]string{old.Lock.Components[0].SkillID: "keep_local"}},
		{Reviewed: true, LocalChoices: map[string]string{old.Lock.Components[0].SkillID: "erase"}},
		{Reviewed: true, LocalChoices: map[string]string{"unrelated": "adopt_upstream"}},
	} {
		req.SessionID = sid
		req.ProposalID = p.ID
		if _, err = a.CapabilityRequest(ctx, "confirm", req); err == nil {
			t.Fatal("accepted incomplete local review")
		}
		pending, err := a.caps().GetProposal(ctx, p.ID)
		if err != nil || pending.Status != "pending" {
			t.Fatal("consumed rejected card", pending, err)
		}
	}
}
func TestCapabilityLocalReconciliationChoices(t *testing.T) {
	for _, choice := range []string{"keep_local", "copy", "adopt_upstream"} {
		t.Run(choice, func(t *testing.T) {
			a, g, sid, old, localVersion := localEditFixture(t, true)
			ctx := context.Background()
			p := proposeLocalUpdate(t, a, g, sid, old)
			skillID := old.Lock.Components[0].SkillID
			value, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true, LocalChoices: map[string]string{skillID: choice}})
			if err != nil {
				t.Fatal(err)
			}
			next := value.(capability.Record)
			component := next.Lock.Components[0]
			if next.LocallyModified != (choice == "keep_local") {
				t.Fatal("confirmation state does not describe retained local content", next)
			}
			if component.SkillID != skillID {
				t.Fatal("lost skill identity")
			}
			pointer, err := a.Ver.GetPointer(ctx, version.EntitySkill, skillID, version.PointerCurrent)
			if err != nil || pointer.String != component.VersionID {
				t.Fatal(pointer, err)
			}
			if choice == "keep_local" {
				if component.VersionID != localVersion || !component.LocallyModified {
					t.Fatal("local version not preserved", component)
				}
			} else if component.VersionID == localVersion || component.LocallyModified {
				t.Fatal("upstream not adopted", component)
			}
			current, err := a.caps().Get(ctx, next.ID)
			if err != nil || current.LocallyModified != (choice == "keep_local") {
				t.Fatal(current, err)
			}
			var total int
			if err = a.DB.QueryRow(`SELECT count(*) FROM skills`).Scan(&total); err != nil {
				t.Fatal(err)
			}
			if choice == "copy" {
				if total != 2 {
					t.Fatal("copy missing", total)
				}
				var copiedVersion string
				if err = a.DB.QueryRow(`SELECT p.version_id FROM skills s JOIN pointers p ON p.entity_type='skill' AND p.entity_id=s.id AND p.name='current' WHERE s.id<>?`, skillID).Scan(&copiedVersion); err != nil {
					t.Fatal(err)
				}
				local, _ := a.Skills.Files(ctx, localVersion)
				copied, _ := a.Skills.Files(ctx, copiedVersion)
				_, want, _ := capability.HashFiles(local)
				_, got, _ := capability.HashFiles(copied)
				if want != got {
					t.Fatal("copy lost local content")
				}
			} else if total != 1 {
				t.Fatal("unexpected duplicate skill", total)
			}
			for _, vid := range []string{old.Lock.Components[0].VersionID, localVersion} {
				if _, err = a.Skills.Files(ctx, vid); err != nil {
					t.Fatal("lost history", err)
				}
			}
			row, err := a.chatSession(ctx, sid)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.verifySessionCapabilities(ctx, row); err != nil {
				t.Fatal("reconciled capability unusable", err)
			}
			events, err := a.caps().Events(ctx, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range events {
				if e.Action == "skill_reconcile" && strings.Contains(e.Result, choice) {
					found = true
				}
			}
			if !found {
				t.Fatal("choice unaudited", events)
			}
		})
	}
}
func TestCapabilityLocalEditAfterReviewRequiresNewProposal(t *testing.T) {
	a, g, sid, old, _ := localEditFixture(t, true)
	ctx := context.Background()
	p := proposeLocalUpdate(t, a, g, sid, old)
	latest := editCapabilitySkill(t, a, old, "An edit made after review.")
	_, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true, LocalChoices: map[string]string{old.Lock.Components[0].SkillID: "adopt_upstream"}})
	var ce *webui.CapabilityError
	if !errors.As(err, &ce) || ce.Code != "local_skills_changed" {
		t.Fatal(err)
	}
	pending, err := a.caps().GetProposal(ctx, p.ID)
	if err != nil || pending.Status != "pending" {
		t.Fatal(pending, err)
	}
	cur, _ := a.Ver.GetPointer(ctx, version.EntitySkill, old.Lock.Components[0].SkillID, version.PointerCurrent)
	if cur.String != latest {
		t.Fatal("overwrote newer edit")
	}
}
func TestCapabilityConcurrentEditCannotBeOverwrittenAtPublication(t *testing.T) {
	a, g, sid, old, _ := localEditFixture(t, true)
	ctx := context.Background()
	p := proposeLocalUpdate(t, a, g, sid, old)
	var err error
	p, err = a.caps().ClaimProposal(ctx, p.ID, sid, "ui")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.proposalFiles(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	_, resolutions, err := a.prepareCapabilitySkills(ctx, &p, files, map[string]string{old.Lock.Components[0].SkillID: "adopt_upstream"})
	if err != nil {
		t.Fatal(err)
	}
	latest := editCapabilitySkill(t, a, old, "Concurrent edit wins.")
	if _, err = a.caps().InstallProposalReconciled(ctx, p.ID, sid, "ui", p.Lock, files, resolutions); !errors.Is(err, capability.ErrLocalSkillsChanged) {
		t.Fatal("publication accepted stale local version", err)
	}
	cur, _ := a.Ver.GetPointer(ctx, version.EntitySkill, old.Lock.Components[0].SkillID, version.PointerCurrent)
	if cur.String != latest {
		t.Fatal("concurrent edit lost")
	}
	rec, err := a.caps().Get(ctx, old.ID)
	if err != nil || rec.VersionID != old.VersionID {
		t.Fatal("partial snapshot adoption", rec, err)
	}
}

func TestCapabilityKeptLocalVersionStillRequiresChoiceNextUpdate(t *testing.T) {
	a, g, sid, old, localVersion := localEditFixture(t, true)
	ctx := context.Background()
	p := proposeLocalUpdate(t, a, g, sid, old)
	skillID := old.Lock.Components[0].SkillID
	v, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true, LocalChoices: map[string]string{skillID: "keep_local"}})
	if err != nil {
		t.Fatal(err)
	}
	kept := v.(capability.Record)
	p = proposeLocalUpdate(t, a, g, sid, kept)
	if len(p.SkillReconciliation) != 1 || !p.SkillReconciliation[0].LocallyModified || p.SkillReconciliation[0].CurrentVersionID != localVersion {
		t.Fatal(p.SkillReconciliation)
	}
	if _, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true}); err == nil {
		t.Fatal("silently replaced retained local version")
	}
}

func TestCapabilityRemovedLocalSkillRemainsIndependent(t *testing.T) {
	for _, choice := range []string{"keep_local", "copy", "adopt_upstream"} {
		t.Run(choice, func(t *testing.T) {
			a, _, sid, old, localVersion := localEditFixture(t, true)
			ctx := context.Background()
			skillID := old.Lock.Components[0].SkillID
			// A bundled update may remove a skill while retaining another component.
			lock := old.Lock
			lock.Components = []capability.Component{{Type: "agent", Name: "remaining agent", Path: "agents/remaining.md"}}
			files := map[string][]byte{"agents/remaining.md": []byte("A replacement agent.")}
			_, lock.FilesSHA256, _ = capability.HashFiles(files)
			lock.Source.Kind = "bundle"
			lock.Source.Adapter = "tidebot-native"
			lock.Scan = capability.ScanFiles(files)
			if err := a.cacheImportedSnapshot(lock.FilesSHA256, files); err != nil {
				t.Fatal(err)
			}
			p, _, err := a.caps().CreateUpdateProposal(ctx, sid, "", lock, files)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.SkillReconciliation) != 1 || !p.SkillReconciliation[0].Removed {
				t.Fatal(p.SkillReconciliation)
			}
			_, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true, LocalChoices: map[string]string{skillID: choice}})
			if err != nil {
				t.Fatal(err)
			}
			current, err := a.Ver.GetPointer(ctx, version.EntitySkill, skillID, version.PointerCurrent)
			if err != nil || current.String != localVersion {
				t.Fatal("removed skill history overwritten", current, err)
			}
			var present int
			if err = a.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, skillID).Scan(&present); err != nil || present != 1 {
				t.Fatal("removed component deleted independent skill", err)
			}
		})
	}
}

func TestCapabilityReconciliationFailureLeavesOriginalPointer(t *testing.T) {
	a, g, sid, old, localVersion := localEditFixture(t, true)
	ctx := context.Background()
	p := proposeLocalUpdate(t, a, g, sid, old)
	var err error
	p, err = a.caps().ClaimProposal(ctx, p.ID, sid, "ui")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.proposalFiles(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	_, resolutions, err := a.prepareCapabilitySkills(ctx, &p, files, map[string]string{old.Lock.Components[0].SkillID: "adopt_upstream"})
	if err != nil {
		t.Fatal(err)
	}
	// Publication must roll back earlier pointer moves if later session adoption fails.
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET status='closed' WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err = a.caps().InstallProposalReconciled(ctx, p.ID, sid, "ui", p.Lock, files, resolutions); err == nil {
		t.Fatal("closed session accepted")
	}
	cur, _ := a.Ver.GetPointer(ctx, version.EntitySkill, old.Lock.Components[0].SkillID, version.PointerCurrent)
	if cur.String != localVersion {
		t.Fatal("failed publication moved skill pointer")
	}
	record, err := a.caps().Get(ctx, old.ID)
	if err != nil || record.VersionID != old.VersionID {
		t.Fatal("failed publication replaced cap", err)
	}
}

func TestCapabilityStoreRejectsMissingAndForgedLocalResolutions(t *testing.T) {
	for _, choice := range []string{"missing", "changed_identity", "missing_copy", "wrong_keep_version"} {
		t.Run(choice, func(t *testing.T) {
			a, g, sid, old, localVersion := localEditFixture(t, true)
			ctx := context.Background()
			p := proposeLocalUpdate(t, a, g, sid, old)
			var err error
			p, err = a.caps().ClaimProposal(ctx, p.ID, sid, "ui")
			if err != nil {
				t.Fatal(err)
			}
			files, err := a.proposalFiles(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			skillID := old.Lock.Components[0].SkillID
			_, resolutions, err := a.prepareCapabilitySkills(ctx, &p, files, map[string]string{skillID: "adopt_upstream"})
			if err != nil {
				t.Fatal(err)
			}
			switch choice {
			case "missing":
				resolutions = nil
			case "changed_identity":
				p.Lock.Components[0].SkillID = "different_skill"
			case "missing_copy":
				resolutions[skillID] = capability.SkillResolution{Action: "copy"}
			case "wrong_keep_version":
				resolutions[skillID] = capability.SkillResolution{Action: "keep_local"}
				p.Lock.Components[0].LocallyModified = true
			}
			if _, err = a.caps().InstallProposalReconciled(ctx, p.ID, sid, "ui", p.Lock, files, resolutions); err == nil {
				t.Fatal("unreviewed local reconciliation accepted", choice)
			}
			cur, _ := a.Ver.GetPointer(ctx, version.EntitySkill, skillID, version.PointerCurrent)
			if cur.String != localVersion {
				t.Fatal("failed reconciliation moved local pointer")
			}
		})
	}
}
