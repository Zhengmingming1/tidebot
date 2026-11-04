package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type fakeCapabilityGit struct {
	files  map[string][]byte
	commit string
	reads  []string
}

func (g *fakeCapabilityGit) Scan(context.Context, string, string, string, string) (skillrepo.Snapshot, error) {
	return skillrepo.Snapshot{Commit: g.commit, Candidates: []skillrepo.Candidate{{Path: "skills/pdf", Name: "PDF"}}}, nil
}
func (g *fakeCapabilityGit) ReadSkill(_ context.Context, _, commit, _ string) (map[string][]byte, error) {
	g.reads = append(g.reads, commit)
	return g.files, nil
}
func capFixture(t *testing.T) (*App, *fakeCapabilityGit, string) {
	t.Helper()
	a := newTestApp(t)
	g := &fakeCapabilityGit{files: map[string][]byte{"SKILL.md": []byte("---\nname: pdf\ndescription: Test document skill\n---\nRead the document.\n")}, commit: strings.Repeat("a", 40)}
	a.capState.git = g
	s, e := a.CreateChatSession(context.Background(), webui.ChatSessionCreate{Origin: "chat"})
	if e != nil {
		t.Fatal(e)
	}
	return a, g, s.ID
}
func TestCapabilityProposalConfirmationPinsAndBinds(t *testing.T) {
	a, g, sid := capFixture(t)
	ctx := context.Background()
	v, e := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	p := v.(capabilityProposalView)
	list, e := a.caps().List(ctx)
	if e != nil || len(list) != 0 {
		t.Fatalf("proposal installed: %+v %v", list, e)
	}
	file, e := a.CapabilityRequest(ctx, "file", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid, Path: "SKILL.md"})
	if e != nil || file.(map[string]any)["untrusted"] != true {
		t.Fatal(file, e)
	}
	g.commit = strings.Repeat("b", 40)
	v, e = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	rec := v.(capability.Record)
	if rec.Lock.Source.Commit != strings.Repeat("a", 40) || g.reads[len(g.reads)-1] != strings.Repeat("a", 40) {
		t.Fatal("moving branch affected approved snapshot")
	}
	row, e := a.chatSession(ctx, sid)
	if e != nil {
		t.Fatal(e)
	}
	bindings := a.chatTurnSkills(ctx, row)
	if len(bindings) != 1 || bindings[0].Mode != "pinned" || bindings[0].VersionID != rec.Lock.Components[0].VersionID {
		t.Fatalf("not pinned %+v", bindings)
	}
	if e = a.caps().Verify(ctx, rec.ID, rec.VersionID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid}); e == nil {
		t.Fatal("replayed confirm")
	}
}
func TestCapabilityProposalRejectsTamperCrossSessionAndExpiry(t *testing.T) {
	for _, test := range []string{"changed", "other_session", "expired"} {
		t.Run(test, func(t *testing.T) {
			a, g, sid := capFixture(t)
			ctx := context.Background()
			v, e := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
			if e != nil {
				t.Fatal(e)
			}
			p := v.(capabilityProposalView)
			req := webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid}
			switch test {
			case "changed":
				g.files["SKILL.md"] = []byte("different content")
			case "other_session":
				s, e := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
				if e != nil {
					t.Fatal(e)
				}
				req.SessionID = s.ID
			case "expired":
				_, e = a.DB.Exec(`UPDATE capability_proposals SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), p.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, e = a.CapabilityRequest(ctx, "confirm", req); e == nil {
				t.Fatal("unsafe confirmation accepted")
			}
			list, e := a.caps().List(ctx)
			if e != nil || len(list) != 0 {
				t.Fatalf("installed %+v %v", list, e)
			}
		})
	}
}
func TestCapabilitySessionTokenCannotInstall(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	env := a.chatToolEnv(sid)
	token := env[sessiontools.EnvToken]
	call := func(tool string) error {
		raw, _ := json.Marshal(map[string]any{"token": token, "tool": tool, "args": map[string]string{}})
		_, e := a.rpcSessionTool(ctx, raw)
		return e
	}
	if e := call("cap_list"); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"cap_install", "confirm", "install", "cap_confirm"} {
		if call(name) == nil {
			t.Fatal("model install exposed")
		}
	}
	a.endChatToolToken(sid)
	if call("cap_list") == nil {
		t.Fatal("expired token accepted")
	}
}
func TestCapabilityUnknownTrustRequiresReview(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	v, e := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "https://github.com/example/skills/tree/main/skills/pdf", SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	p := v.(capabilityProposalView)
	_, e = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid})
	var ce *webui.CapabilityError
	if !errors.As(e, &ce) || ce.Code != "review_required" {
		t.Fatal(e)
	}
	if _, e = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid, Reviewed: true}); e != nil {
		t.Fatal(e)
	}
}
func TestCapabilityMetadataAndRegisteredDiscovery(t *testing.T) {
	got := inferCapabilityMetadata([]byte("---\nmetadata:\n  requires:\n    bins: [dws, '../bad']\n  cli_version: '>=1.0.61'\n---\nHello"))
	if len(got) != 1 || got[0].Name != "dws" || got[0].Version != ">=1.0.61" {
		t.Fatalf("%+v", got)
	}
	a, _, _ := capFixture(t)
	ctx := context.Background()
	repo, e := a.Skills.SaveRepository(ctx, skillstore.Repository{Name: "Example", URL: "https://github.com/example/skills"})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal([]skillrepo.Candidate{{Path: "skills/pdf", Name: "PDF", Description: "documents"}})
	if e = a.Skills.RecordRepositoryScan(ctx, repo.ID, repo.Revision, strings.Repeat("a", 40), raw, ""); e != nil {
		t.Fatal(e)
	}
	results, e := a.searchCapabilities(ctx, "documents")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range results {
		if strings.HasPrefix(r.ID, "repository:") {
			found = true
			if _, e = a.inspectCapability(ctx, r.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	if !found {
		t.Fatal("registered source absent")
	}
}
func TestCapabilityInstalledBytesMustMatchReviewedSnapshot(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	v, e := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: "pdf", SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	p := v.(capabilityProposalView)
	v, e = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid})
	if e != nil {
		t.Fatal(e)
	}
	rec := v.(capability.Record)
	_, e = a.DB.Exec(`UPDATE blobs SET data=? WHERE sha256 IN (SELECT sha256 FROM version_files WHERE version_id=?)`, []byte("tampered"), rec.Lock.Components[0].VersionID)
	if e != nil {
		t.Fatal(e)
	}
	row, e := a.chatSession(ctx, sid)
	if e != nil {
		t.Fatal(e)
	}
	if len(a.chatTurnSkills(ctx, row)) != 0 {
		t.Fatal("staged unreviewed bytes")
	}
}
