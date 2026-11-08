package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// bigProposalPayload is a lock with scan findings, about kb KiB of JSON, the
// shape a real install card stores.
func bigProposalPayload(t testing.TB, kb int) string {
	t.Helper()
	l := capability.Lock{Format: capability.LockFormat, ID: "demo", Title: "Demo", Scan: capability.Scan{Scanner: "tidebot", Verdict: "warn"}}
	for i := 0; len(mustJSON(t, l)) < kb<<10; i++ {
		l.Scan.Findings = append(l.Scan.Findings, capability.Finding{ID: fmt.Sprint("f", i), File: "skills/demo/scripts/run.sh", Line: i, Note: strings.Repeat("network call ", 8), Severity: "warn"})
	}
	return mustJSON(t, l)
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listSessionProposals(t testing.TB, a *App, sid, since string) map[string]any {
	t.Helper()
	v, err := a.CapabilityRequest(context.Background(), "proposals", webui.CapabilityRequest{SessionID: sid, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	out["_bytes"] = len(b)
	return out
}

// The chat page asks for a session's cards on every 1.5 s poll while a turn is
// being answered. Unchanged cards must not be read, decoded and sent again.
func TestSessionProposalsSinceUnchanged(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	addProposalRow(t, a, "p1", ses.ID, "pending", now.Add(-time.Minute), bigProposalPayload(t, 8))
	first := listSessionProposals(t, a, ses.ID, "")
	sig, _ := first["sig"].(string)
	if sig == "" || len(first["proposals"].([]any)) != 1 {
		t.Fatalf("first list: sig=%q proposals=%v", sig, first["proposals"])
	}
	again := listSessionProposals(t, a, ses.ID, sig)
	if again["unchanged"] != true || again["proposals"] != nil || again["sig"] != sig {
		t.Fatalf("unchanged list should be a short reply: %v", again)
	}
	if again["_bytes"].(int) > 200 {
		t.Fatalf("unchanged reply is %d bytes", again["_bytes"])
	}
	// a new card, a decision, and a stale or forged sig all send the full list
	addProposalRow(t, a, "p2", ses.ID, "pending", now, "{}")
	added := listSessionProposals(t, a, ses.ID, sig)
	if added["unchanged"] == true || len(added["proposals"].([]any)) != 2 || added["sig"] == sig {
		t.Fatalf("new card not sent: %v", added["sig"])
	}
	sig2 := added["sig"].(string)
	if _, err := a.DB.Exec(`UPDATE capability_proposals SET status='dismissed',decided_at=? WHERE id='p1'`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	decided := listSessionProposals(t, a, ses.ID, sig2)
	if decided["unchanged"] == true || decided["sig"] == sig2 {
		t.Fatal("status change not sent")
	}
	if forged := listSessionProposals(t, a, ses.ID, "nope"); forged["unchanged"] == true {
		t.Fatal("unknown sig treated as unchanged")
	}
	// another session's cards never match this session's sig
	other, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	empty := listSessionProposals(t, a, other.ID, "")
	if o := listSessionProposals(t, a, other.ID, decided["sig"].(string)); o["unchanged"] == true {
		t.Fatal("sig leaked across sessions")
	}
	if e := listSessionProposals(t, a, other.ID, empty["sig"].(string)); e["unchanged"] != true {
		t.Fatal("empty list sig not reused")
	}
	// the sig query reads the index only, never the payload rows
	plan := ""
	rows, err := a.DB.Query(`EXPLAIN QUERY PLAN `+capability.ProposalSigQuery, ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		_ = rows.Scan(&id, &parent, &unused, &detail)
		plan += detail + ";"
	}
	rows.Close()
	if !strings.Contains(plan, "COVERING INDEX") {
		t.Fatalf("sig query is not index-only: %s", plan)
	}
}

func TestSessionProposalsPollPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("TIDEBOT_PERF=1")
	}
	a := newTestApp(t)
	ctx := context.Background()
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	payload := bigProposalPayload(t, 64)
	now := time.Now()
	for i := 0; i < 20; i++ {
		addProposalRow(t, a, fmt.Sprint("p", i), ses.ID, "pending", now.Add(time.Duration(i)*time.Second), payload)
	}
	first := listSessionProposals(t, a, ses.ID, "")
	sig, _ := first["sig"].(string)
	for _, c := range []struct{ name, since string }{{"full", ""}, {"since", sig}} {
		best := time.Hour
		var size int
		for r := 0; r < 5; r++ {
			start := time.Now()
			for i := 0; i < 20; i++ {
				size = listSessionProposals(t, a, ses.ID, c.since)["_bytes"].(int)
			}
			if d := time.Since(start) / 20; d < best {
				best = d
			}
		}
		t.Logf("%s: %v per poll, %d bytes", c.name, best, size)
	}
}
