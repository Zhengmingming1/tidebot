package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func grantFixture(t *testing.T) (*App, capability.Record, string, webui.CapabilityGrantRequest) {
	t.Helper()
	a, _, sid := capFixture(t)
	ctx := context.Background()
	l := capability.Lock{Format: capability.LockFormat, ID: "grant-example", Title: "Grant example", Source: capability.Source{Kind: "bundle", Trust: "unknown", Adapter: "skill-dir"}, Components: []capability.Component{{Type: "mcp", Name: "server", MCP: &capability.MCPServer{Type: "http", URL: "https://example.com/mcp"}}}}
	r, err := a.caps().Install(ctx, l, map[string][]byte{"README.md": []byte("inert")})
	if err != nil {
		t.Fatal(err)
	}
	bindings, _ := json.Marshal(map[string]any{"capabilities": []string{r.ID}})
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET bindings_json=? WHERE id=?`, string(bindings), sid); err != nil {
		t.Fatal(err)
	}
	tools := []adapters.MCPTool{{Name: "lookup", Description: "Read records", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "delete", Description: "Delete records", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if err = a.saveCapabilityDiscovery(ctx, r, r.Lock.Components[0], tools); err != nil {
		t.Fatal(err)
	}
	views, err := a.capabilityGrantViews(ctx, sid)
	if err != nil || len(views) != 1 {
		t.Fatal(views, err)
	}
	return a, r, sid, webui.CapabilityGrantRequest{SessionID: sid, CapabilityID: r.ID, VersionID: r.VersionID, Component: "server", DiscoveryHash: views[0].DiscoveryHash, Tools: []string{"lookup"}}
}
func TestCapabilityGrantsRequireExactReviewAndScope(t *testing.T) {
	a, r, sid, req := grantFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"*", "unknown", "lookup,delete", "lookup(*)"} {
		v := req
		v.Tools = []string{bad}
		if _, err := a.CapabilityGrantRequest(ctx, "PUT", v); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	stale := req
	stale.DiscoveryHash = "stale"
	if _, err := a.CapabilityGrantRequest(ctx, "PUT", stale); err == nil {
		t.Fatal("accepted stale metadata")
	}
	other, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	unbound := req
	unbound.SessionID = other.ID
	if _, err = a.CapabilityGrantRequest(ctx, "PUT", unbound); err == nil {
		t.Fatal("cross-session grant")
	}
	if _, err = a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
		t.Fatal(err)
	}
	plain := runner.Options{KeepSession: true, RunMode: "read"}
	close, err := a.applyCapabilityMCP(ctx, &plain, r)
	if err != nil {
		t.Fatal(err)
	}
	close()
	if len(plain.MCPServers) != 0 {
		t.Fatal("unscoped grant escaped")
	}
	opts := runner.Options{KeepSession: true, RunMode: "read", ContextArgs: []string{"--disallowedTools", "Bash,Write,Edit"}}
	close, err = a.applySessionCapabilityMCP(ctx, &opts, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if len(opts.MCPServers) != 1 {
		t.Fatal("reviewed server unavailable")
	}
	allowed := argAfter(opts.ContextArgs, "--allowedTools")
	denied := argAfter(opts.ContextArgs, "--disallowedTools")
	if strings.Count(allowed, "__lookup") != 1 {
		t.Fatalf("duplicate MCP grants: %s", allowed)
	}

	if strings.Contains(allowed, "*") || !strings.HasSuffix(allowed, "__lookup") || !strings.Contains(denied, "__delete") || !strings.Contains(denied, "Bash,Write,Edit") {
		t.Fatal(opts.ContextArgs)
	}
	if _, err = a.CapabilityGrantRequest(ctx, "DELETE", req); err != nil {
		t.Fatal(err)
	}
	if opts.AtStart(ctx, &runner.Options{RunID: "unused"}) == nil {
		t.Fatal("queued revoked grant allowed")
	}
}
func TestCapabilityGrantMetadataAndVersionChangesRevoke(t *testing.T) {
	a, r, sid, req := grantFixture(t)
	ctx := context.Background()
	if _, err := a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
		t.Fatal(err)
	}
	changed := []adapters.MCPTool{{Name: "lookup", Description: "Now changes records", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if err := a.saveCapabilityDiscovery(ctx, r, r.Lock.Components[0], changed); err != nil {
		t.Fatal(err)
	}
	views, err := a.capabilityGrantViews(ctx, sid)
	if err != nil || len(views[0].GrantedTools) != 0 {
		t.Fatal(views, err)
	}
	req.DiscoveryHash = views[0].DiscoveryHash
	if _, err = a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
		t.Fatal(err)
	}
	if _, err = a.caps().Install(ctx, r.Lock, map[string][]byte{"README.md": []byte("inert")}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.DB.QueryRow(`SELECT count(*) FROM capability_tool_grants WHERE capability_id=?`, r.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("version change retained grants", count, err)
	}
	if _, err = a.caps().Rollback(ctx, r.ID, r.VersionID, "ui"); err != nil {
		t.Fatal(err)
	}
	views, err = a.capabilityGrantViews(ctx, sid)
	if err != nil || len(views[0].GrantedTools) != 0 {
		t.Fatal("rollback reactivated grants", views, err)
	}
}

func TestCapabilityGrantLaunchPinsAndRechecks(t *testing.T) {
	for _, change := range []string{"none", "unbind", "discovery", "version"} {
		t.Run(change, func(t *testing.T) {
			a, r, sid, req := grantFixture(t)
			ctx := context.Background()
			if _, err := a.CapabilityGrantRequest(ctx, "PUT", req); err != nil {
				t.Fatal(err)
			}
			opts := runner.Options{RunID: "grant_run", KeepSession: true, RunMode: "read"}
			close, err := a.applySessionCapabilityMCP(ctx, &opts, sid)
			if err != nil {
				t.Fatal(err)
			}
			defer close()
			if _, err = a.DB.Exec(`INSERT INTO runs(id,created_at) VALUES(?,?)`, opts.RunID, nowUTC()); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "unbind":
				_, err = a.DB.Exec(`UPDATE chat_sessions SET bindings_json='{}' WHERE id=?`, sid)
			case "discovery":
				err = a.saveCapabilityDiscovery(ctx, r, r.Lock.Components[0], nil)
			case "version":
				_, err = a.caps().Install(ctx, r.Lock, map[string][]byte{"README.md": []byte("inert")})
			}
			if err != nil {
				t.Fatal(err)
			}
			err = opts.AtStart(ctx, &opts)
			if change == "none" && err != nil {
				t.Fatal(err)
			}
			if change != "none" && err == nil {
				t.Fatal("changed grant launched")
			}
			var count int
			if err = a.DB.QueryRow(`SELECT count(*) FROM capability_run_bindings WHERE run_id=?`, opts.RunID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if change == "none" {
				want = 1
			}
			if count != want {
				t.Fatal("wrong pin count", count, want)
			}
		})
	}
}
