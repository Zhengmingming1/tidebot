package app

import (
	"context"
	"encoding/json"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"net/http"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func lifecycleProposalFixture(t *testing.T) (*App, capability.Record, string) {
	t.Helper()
	a, _, sid := capFixture(t)
	r, err := a.caps().Install(context.Background(), capability.Lock{Format: capability.LockFormat, ID: "lifecycle-test", Title: "Lifecycle", Source: capability.Source{Kind: "bundle", Trust: "unknown", Adapter: "skill-dir"}, Components: []capability.Component{{Type: "mcp", Name: "server", MCP: &capability.MCPServer{Type: "http", URL: "https://example.com/mcp"}}}}, map[string][]byte{"README.md": []byte("inert")})
	if err != nil {
		t.Fatal(err)
	}
	return a, r, sid
}
func TestCapabilityUninstallProposalIsReadOnlyAndExact(t *testing.T) {
	a, r, sid := lifecycleProposalFixture(t)
	ctx := context.Background()
	v, err := a.proposeCapabilityLifecycle(ctx, "uninstall", webui.CapabilityRequest{Source: r.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	p := v.(capabilityProposalView).Proposal
	if p.BaseVersionID != r.VersionID || p.UninstallPreview == nil || len(p.UninstallPreview.Retained) != 3 {
		t.Fatalf("missing exact summary: %+v", p)
	}
	if _, err = a.caps().Get(ctx, r.ID); err != nil {
		t.Fatal("proposal uninstalled capability", err)
	}
	persisted, err := a.caps().GetProposal(ctx, p.ID)
	if err != nil || persisted.UninstallPreview == nil {
		t.Fatal("summary not persisted", err)
	}
	req := webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true}
	if _, err = a.decideCapabilityUninstall(ctx, "confirm", req, p); err == nil {
		t.Fatal("accepted missing exact version")
	}
	req.VersionID = r.VersionID
	if _, err = a.decideCapabilityUninstall(ctx, "confirm", req, p); err != nil {
		t.Fatal(err)
	}
	if _, err = a.caps().Get(ctx, r.ID); err == nil {
		t.Fatal("owner confirmation did not uninstall")
	}
	if _, err = a.decideCapabilityUninstall(ctx, "confirm", req, p); err == nil {
		t.Fatal("replayed confirmation")
	}
}
func TestCapabilityUninstallProposalBlocksBindingsAndStaleVersion(t *testing.T) {
	a, r, sid := lifecycleProposalFixture(t)
	ctx := context.Background()
	if err := a.caps().Bind(ctx, capability.Binding{OwnerKind: "task", OwnerID: "fixture", CapabilityID: r.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.proposeCapabilityLifecycle(ctx, "uninstall", webui.CapabilityRequest{Source: r.ID, SessionID: sid}); err == nil {
		t.Fatal("accepted bound uninstall")
	}
	if _, err := a.DB.Exec(`DELETE FROM capability_bindings WHERE capability_id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	v, err := a.proposeCapabilityLifecycle(ctx, "uninstall", webui.CapabilityRequest{Source: r.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	p := v.(capabilityProposalView).Proposal
	r.Lock.FilesSHA256 = ""
	next, err := a.caps().Install(ctx, r.Lock, map[string][]byte{"README.md": []byte("next")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.decideCapabilityUninstall(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, Reviewed: true, VersionID: r.VersionID}, p); err == nil {
		t.Fatal("accepted stale version")
	}
	if got, err := a.caps().Get(ctx, r.ID); err != nil || got.VersionID != next.VersionID {
		t.Fatal("changed current version", err)
	}
}

func TestCapabilityLifecycleRPCOnlyProposes(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	latest := "1.0.0"
	fixture := registryFixtureApp(t, func(r *http.Request) (*http.Response, error) {
		return registryFixtureResponse(registryFixtureRecord("com.example/fixture", latest)), nil
	})
	a.capState.registryClient = fixture.capState.registryClient
	lock, err := a.inspectCapabilityRegistry(ctx, "mcp-registry:com.example/fixture")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.proposalFiles(ctx, capability.Proposal{Lock: lock})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := a.caps().Install(ctx, lock, files)
	if err != nil {
		t.Fatal(err)
	}
	token := a.chatToolEnv(sid)[sessiontools.EnvToken]
	call := func(action string) (any, error) {
		raw, _ := json.Marshal(map[string]any{"token": token, "tool": "cap_propose", "args": map[string]string{"action": action, "source": installed.ID}})
		return a.rpcSessionTool(ctx, raw)
	}
	latest = "2.0.0"
	for _, action := range []string{"update", "uninstall"} {
		value, err := call(action)
		if err != nil {
			t.Fatal(action, err)
		}
		var p capability.Proposal
		if err = json.Unmarshal([]byte(value.(map[string]string)["text"]), &p); err != nil || p.Kind != action || p.Status != "pending" || p.BaseVersionID != installed.VersionID {
			t.Fatalf("bad %s proposal: %+v %v", action, p, err)
		}
		if action == "update" {
			reloaded, err := a.caps().GetProposal(ctx, p.ID)
			if err != nil || reloaded.Diff == nil || reloaded.Diff.FromVersion != installed.VersionID {
				t.Fatalf("update review diff lost after reload: %+v %v", reloaded, err)
			}
			view, err := a.CapabilityRequest(ctx, "proposals", webui.CapabilityRequest{SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(view)
			if !strings.Contains(string(raw), `"diff"`) {
				t.Fatal("proposal listing lost review diff")
			}
		}
		got, err := a.caps().Get(ctx, installed.ID)
		if err != nil || got.VersionID != installed.VersionID {
			t.Fatalf("model changed capability: %+v %v", got, err)
		}
	}
	for _, action := range []string{"confirm", "adopt", "delete"} {
		if _, err = call(action); err == nil {
			t.Fatal("model action accepted", action)
		}
	}
}

func TestCapabilityUninstallProposalRejectsChangedSummaryAndLateBinding(t *testing.T) {
	for _, change := range []string{"summary", "binding"} {
		t.Run(change, func(t *testing.T) {
			a, r, sid := lifecycleProposalFixture(t)
			ctx := context.Background()
			v, err := a.proposeCapabilityLifecycle(ctx, "uninstall", webui.CapabilityRequest{Source: r.ID, SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			p := v.(capabilityProposalView).Proposal
			if change == "binding" {
				if err = a.caps().Bind(ctx, capability.Binding{OwnerKind: "task", OwnerID: "late", CapabilityID: r.ID}); err != nil {
					t.Fatal(err)
				}
				if _, err = a.decideCapabilityUninstall(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, VersionID: r.VersionID, Reviewed: true}, p); err == nil {
					t.Fatal("accepted late reference")
				}
			} else {
				summary := *p.UninstallPreview
				summary.RemovedSkills = []string{"not-reviewed"}
				if _, err = a.caps().UninstallVersionWithReport(ctx, r.ID, r.VersionID, "ui", summary); err == nil {
					t.Fatal("accepted changed deletion summary")
				}
			}
			if _, err = a.caps().Get(ctx, r.ID); err != nil {
				t.Fatal("removed despite failed review", err)
			}
		})
	}
}

// End to end through the session card: a capability whose skill was installed by
// a confirmed card, unbound by the owner, then uninstalled through an uninstall
// card. Confirming must remove the exclusive skill instead of failing with
// "uninstall contents changed".
func TestCapabilityUninstallCardRemovesExclusiveSkill(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	v, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: v.(capabilityProposalView).ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	rec := v.(capability.Record)
	skillID := rec.Lock.Components[0].SkillID
	if skillID == "" {
		t.Fatal("install did not register a skill")
	}
	if _, err = a.DB.Exec(`UPDATE chat_sessions SET bindings_json='[]' WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`DELETE FROM capability_bindings WHERE capability_id=?`, rec.ID); err != nil {
		t.Fatal(err)
	}
	v, err = a.proposeCapabilityLifecycle(ctx, "uninstall", webui.CapabilityRequest{Source: rec.ID, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	p := v.(capabilityProposalView).Proposal
	if len(p.UninstallPreview.RemovedSkills) != 1 || p.UninstallPreview.RemovedSkills[0] != skillID {
		t.Fatalf("summary should remove the skill: %+v", p.UninstallPreview)
	}
	out, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid, Reviewed: true, VersionID: rec.VersionID})
	if err != nil {
		t.Fatalf("confirmed uninstall failed: %v", err)
	}
	if res, ok := out.(capability.UninstallResult); !ok || len(res.RemovedSkills) != 1 {
		t.Fatalf("result: %#v", out)
	}
	if _, err = a.caps().Get(ctx, rec.ID); err == nil {
		t.Fatal("capability still installed")
	}
	var n int
	if err = a.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, skillID).Scan(&n); err != nil || n != 0 {
		t.Fatal("skill registration kept", n, err)
	}
	got, err := a.caps().GetProposal(ctx, p.ID)
	if err != nil || got.Status != "applied" {
		t.Fatalf("proposal status %q %v", got.Status, err)
	}
}
