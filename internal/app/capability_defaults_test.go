package app

import (
	"context"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestCapabilityDefaultsOnlyNewSessionsWithoutGrants(t *testing.T) {
	a, r, old, req := grantFixture(t)
	ctx := context.Background()
	if _, err := a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetCapabilityDefault(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetCapabilityDefault(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	defaults, err := a.CapabilityDefaults(ctx)
	if err != nil || len(defaults) != 1 || defaults[0] != r.ID {
		t.Fatal(defaults, err)
	}
	s, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Bindings) != 1 || s.Bindings[0].ID != r.ID {
		t.Fatal(s.Bindings)
	}
	var count int
	if err = a.DB.QueryRow(`SELECT count(*) FROM capability_bindings WHERE owner_kind='session' AND owner_id=? AND capability_id=?`, s.ID, r.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = a.DB.QueryRow(`SELECT count(*) FROM capability_tool_grants WHERE session_id=?`, s.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("default granted tools", count, err)
	}
	if _, err = a.SetCapabilityDefault(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	fresh, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil || len(fresh.Bindings) != 0 {
		t.Fatal(fresh, err)
	}
	existing, err := a.chatSession(ctx, s.ID)
	if err != nil || len(existing.Bindings) != 1 {
		t.Fatal("toggle changed existing session", existing, err)
	}
	if err = a.DB.QueryRow(`SELECT count(*) FROM capability_tool_grants WHERE session_id=?`, old).Scan(&count); err != nil || count != 1 {
		t.Fatal("toggle revoked existing grant", count, err)
	}
}

func TestCapabilityDeactivateClearsBindingsNotFilesOrHistory(t *testing.T) {
	a, r, sid, req := grantFixture(t)
	ctx := context.Background()
	if _, err := a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetCapabilityDefault(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"task", "workflow_node", "button", "session"} {
		if err := a.caps().Bind(ctx, capability.Binding{OwnerKind: kind, OwnerID: sid, CapabilityID: r.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.Exec(`INSERT INTO runs(id,created_at,status) VALUES('historical-cap-run',?,'succeeded')`, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO capability_run_bindings(run_id,capability_id,version_id) VALUES('historical-cap-run',?,?)`, r.ID, r.VersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeactivateCapability(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"capability_bindings", "capability_tool_grants", "capability_session_defaults"} {
		var count int
		if err := a.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE capability_id=?`, r.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	row, err := a.chatSession(ctx, sid)
	if err != nil || len(parseChatBindings(row.bindingsRaw).Capabilities) != 0 {
		t.Fatal(row, err)
	}
	if err = a.caps().Verify(ctx, r.ID, r.VersionID); err != nil {
		t.Fatal("files removed", err)
	}
	var count int
	if err = a.DB.QueryRow(`SELECT count(*) FROM capability_run_bindings WHERE capability_id=?`, r.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("history lost", count, err)
	}
}

func TestCapabilityDeactivateBusyIsAtomic(t *testing.T) {
	for _, kind := range []string{"session", "run"} {
		t.Run(kind, func(t *testing.T) {
			a, r, sid, req := grantFixture(t)
			ctx := context.Background()
			if _, err := a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
				t.Fatal(err)
			}
			if _, err := a.SetCapabilityDefault(ctx, r.ID, true); err != nil {
				t.Fatal(err)
			}
			if kind == "session" {
				if _, err := a.DB.Exec(`UPDATE chat_sessions SET status='running' WHERE id=?`, sid); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.DB.Exec(`INSERT INTO runs(id,created_at,status) VALUES('busy-cap-run',?,'running')`, nowUTC()); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(`INSERT INTO capability_run_bindings(run_id,capability_id,version_id) VALUES('busy-cap-run',?,?)`, r.ID, r.VersionID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.DeactivateCapability(ctx, r.ID); err == nil {
				t.Fatal("deactivated busy capability")
			}
			var count int
			if err := a.DB.QueryRow(`SELECT count(*) FROM capability_tool_grants WHERE capability_id=?`, r.ID).Scan(&count); err != nil || count != 1 {
				t.Fatal("partial revoke", count, err)
			}
			ds, err := a.CapabilityDefaults(ctx)
			if err != nil || len(ds) != 1 {
				t.Fatal("partial defaults clear", ds, err)
			}
		})
	}
}

func TestCapabilitySetChatBindingsSyncsAndRevokes(t *testing.T) {
	a, r, sid, req := grantFixture(t)
	ctx := context.Background()
	if _, err := a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetChatBindings(ctx, sid, webui.ChatBindingsInput{Capabilities: []string{r.ID}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.DB.QueryRow(`SELECT count(*) FROM capability_bindings WHERE owner_id=?`, sid).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := a.SetChatBindings(ctx, sid, webui.ChatBindingsInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetChatBindings(ctx, sid, webui.ChatBindingsInput{Capabilities: []string{r.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(`SELECT count(*) FROM capability_tool_grants WHERE session_id=?`, sid).Scan(&count); err != nil || count != 0 {
		t.Fatal("grant revived on rebind", count, err)
	}
}

func TestCapabilityDefaultsApplyToRunContinuation(t *testing.T) {
	a, r, _, _ := grantFixture(t)
	ctx := context.Background()
	if _, err := a.SetCapabilityDefault(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO runs(id,created_at,status) VALUES('default-origin-run',?,'succeeded')`, nowUTC()); err != nil {
		t.Fatal(err)
	}
	s, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: "default-origin-run"})
	if err != nil || len(s.Bindings) != 1 {
		t.Fatal(s, err)
	}
	if _, err = a.SetCapabilityDefault(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	same, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: "default-origin-run"})
	if err != nil || same.ID != s.ID || len(same.Bindings) != 1 {
		t.Fatal(same, err)
	}
}

func TestCapabilitySessionJSONSyncAndDeletion(t *testing.T) {
	a, r, sid, _ := grantFixture(t)
	var count int
	if err := a.DB.QueryRow(`SELECT count(*) FROM capability_bindings WHERE owner_kind='session' AND owner_id=? AND capability_id=?`, sid, r.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("JSON binding not normalized", count, err)
	}
	if _, err := a.DB.Exec(`DELETE FROM chat_sessions WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(`SELECT count(*) FROM capability_bindings WHERE owner_kind='session' AND owner_id=?`, sid).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan session binding", count, err)
	}
}

func TestCapabilityDefaultsRejectMissingAndClearOnUninstall(t *testing.T) {
	a, r, sid, _ := grantFixture(t)
	ctx := context.Background()
	if _, err := a.SetCapabilityDefault(ctx, "missing-capability", true); err == nil {
		t.Fatal("missing capability accepted")
	}
	if _, err := a.SetCapabilityDefault(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetChatBindings(ctx, sid, webui.ChatBindingsInput{}); err != nil {
		t.Fatal(err)
	}
	if err := a.caps().Uninstall(ctx, r.ID, "ui"); err != nil {
		t.Fatal(err)
	}
	defaults, err := a.CapabilityDefaults(ctx)
	if err != nil || len(defaults) != 0 {
		t.Fatal(defaults, err)
	}
	var count int
	if err = a.DB.QueryRow(`SELECT count(*) FROM capability_session_defaults`).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale default", count, err)
	}
}
