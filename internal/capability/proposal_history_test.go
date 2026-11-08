package capability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// insertProposal writes a proposal row whose payload pins skillID, as an update
// or skill_optimize proposal does.
func insertProposal(t *testing.T, s *Store, id, session, status string, expires time.Time, skillID string) {
	t.Helper()
	l := testLock()
	l.Components[0].SkillID = skillID
	raw, err := json.Marshal(proposalPayload{Lock: l})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB.Exec(`INSERT INTO capability_proposals(id,session_id,run_id,kind,payload_json,status,created_at,expires_at,base_version_id) VALUES(?,?,'','update',?,?,?,?,'')`,
		id, session, string(raw), status, now, expires.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func insertChatSession(t *testing.T, s *Store, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB.Exec(`INSERT INTO chat_sessions(id,created_at,updated_at) VALUES(?,?,?)`, id, now, now); err != nil {
		t.Fatal(err)
	}
}

func retainedByHistory(out UninstallResult, skillID string) bool {
	for _, r := range out.Retained {
		if r.Kind == "skill" && r.ID == skillID && strings.Contains(r.Reason, "proposal history") {
			return true
		}
	}
	return false
}

// A pending proposal past its 24 h expiry can never be confirmed (ClaimProposal
// checks expires_at), and one whose session was deleted can never be reached.
// Neither may keep an uninstalled capability's skill registration alive.
func TestUninstallIgnoresDeadProposals(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	sid := r.Lock.Components[0].SkillID
	insertChatSession(t, s, "chat_live")
	insertProposal(t, s, "capprop_expired", "chat_live", "pending", time.Now().Add(-time.Hour), sid)
	insertProposal(t, s, "capprop_orphan", "chat_deleted", "pending", time.Now().Add(time.Hour), sid)
	insertProposal(t, s, "capprop_done", "chat_live", "dismissed", time.Now().Add(time.Hour), sid)
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if retainedByHistory(out, sid) || len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != sid {
		t.Fatalf("dead proposals kept the skill: %+v", out)
	}
}

// A live pending proposal (session exists, not expired) and one being applied
// still retain the skill.
func TestUninstallKeepsLiveProposalReferences(t *testing.T) {
	for _, tc := range []struct{ status string }{{"pending"}, {"applying"}} {
		t.Run(tc.status, func(t *testing.T) {
			s := testStore(t)
			r, _ := installOwnedSkill(t, s)
			sid := r.Lock.Components[0].SkillID
			insertChatSession(t, s, "chat_live")
			insertProposal(t, s, "capprop_live", "chat_live", tc.status, time.Now().Add(time.Hour), sid)
			out, err := s.UninstallWithReport(context.Background(), r.ID, "ui")
			if err != nil {
				t.Fatal(err)
			}
			if !retainedByHistory(out, sid) {
				t.Fatalf("live %s proposal must retain the skill: %+v", tc.status, out)
			}
		})
	}
}
