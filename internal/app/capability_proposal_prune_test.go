package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func addProposalRow(t *testing.T, a *App, id, session, status string, created time.Time, payload string) {
	t.Helper()
	c := created.UTC()
	if _, err := a.DB.Exec(`INSERT INTO capability_proposals(id,session_id,run_id,kind,payload_json,status,created_at,expires_at,base_version_id) VALUES(?,?,'','install',?,?,?,?,'')`,
		id, session, payload, status, c.Format(time.RFC3339Nano), c.Add(24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func proposalIDs(t *testing.T, a *App) string {
	t.Helper()
	rows, err := a.DB.Query(`SELECT id FROM capability_proposals ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

// Proposals used to stay forever: an expired pending card keeps status
// 'pending', and deleting a session left its cards behind.
func TestPruneRowsCapabilityProposals(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	addProposalRow(t, a, "p_live_new", ses.ID, "pending", now.Add(-time.Hour), "{}")        // live card: stays
	addProposalRow(t, a, "p_live_applied", ses.ID, "applied", now.Add(-10*day), "{}")       // recent history: stays
	addProposalRow(t, a, "p_live_expired", ses.ID, "pending", now.Add(-40*day), "{}")       // expired long ago: goes
	addProposalRow(t, a, "p_live_dismissed", ses.ID, "dismissed", now.Add(-40*day), "{}")   // settled long ago: goes
	addProposalRow(t, a, "p_live_applying", ses.ID, "applying", now.Add(-40*day), "{}")     // being applied: stays
	addProposalRow(t, a, "p_orphan_old", "chat_gone", "pending", now.Add(-2*day), "{}")     // session gone: goes
	addProposalRow(t, a, "p_orphan_new", "chat_gone", "pending", now.Add(-time.Hour), "{}") // gone, within a day: stays
	addProposalRow(t, a, "p_orphan_applying", "chat_gone", "applying", now.Add(-2*day), "{}")
	n, err := a.pruneRows(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("deleted %d rows, want 3", n)
	}
	if got := proposalIDs(t, a); got != "p_live_applied,p_live_applying,p_live_new,p_orphan_applying,p_orphan_new" {
		t.Fatalf("proposals left: %s", got)
	}
}

func TestDeleteChatSessionDropsItsProposals(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	mk := func() string {
		s, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	gone, kept := mk(), mk()
	now := time.Now()
	addProposalRow(t, a, "p_gone_pending", gone, "pending", now, "{}")
	addProposalRow(t, a, "p_gone_applied", gone, "applied", now, "{}")
	addProposalRow(t, a, "p_gone_applying", gone, "applying", now, "{}")
	addProposalRow(t, a, "p_kept", kept, "pending", now, "{}")
	if err := a.DeleteChatSession(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if got := proposalIDs(t, a); got != "p_gone_applying,p_kept" {
		t.Fatalf("proposals left: %s", got)
	}
}
