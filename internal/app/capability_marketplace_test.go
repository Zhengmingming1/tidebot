package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type marketplaceFakeGit struct {
	commit    string
	snapshots map[string]map[string][]byte
	scans     []marketplaceSourceReference
	reads     []marketplaceSourceReference
}

func (g *marketplaceFakeGit) Scan(_ context.Context, _, u, ref, root string) (skillrepo.Snapshot, error) {
	g.scans = append(g.scans, marketplaceSourceReference{URL: u, Ref: ref, Root: root})
	commit := g.commit
	if marketplaceCommit(ref) {
		commit = ref
	}
	return skillrepo.Snapshot{Commit: commit}, nil
}
func (g *marketplaceFakeGit) ReadSkill(context.Context, string, string, string) (map[string][]byte, error) {
	return nil, errors.New("unexpected skill read")
}
func (g *marketplaceFakeGit) ReadSnapshot(_ context.Context, _, commit, root string) (map[string][]byte, error) {
	g.reads = append(g.reads, marketplaceSourceReference{Commit: commit, Root: root})
	files, ok := g.snapshots[root]
	if !ok {
		return nil, errors.New("snapshot absent")
	}
	return files, nil
}
func TestMarketplaceListSelectProposeConfirm(t *testing.T) {
	for _, manifest := range []string{".claude-plugin/marketplace.json", "marketplace.json"} {
		t.Run(manifest, func(t *testing.T) {
			a, _, sid := capFixture(t)
			ctx := context.Background()
			commit := strings.Repeat("a", 40)
			g := &marketplaceFakeGit{commit: commit, snapshots: map[string]map[string][]byte{
				".": {manifest: []byte(`{"plugins":[{"name":"local","source":"./plugins/local"},{"name":"remote","source":{"source":"github","repo":"third-party/plugin","ref":"main"}}]}`)},
				"plugins/local": {
					".claude-plugin/plugin.json": []byte(`{"name":"local","hooks":{"ignored":true}}`),
					"skills/demo/SKILL.md":       []byte("---\nname: demo\ndescription: A useful demo\n---\nRead a document.\n"),
				},
			}}
			a.capState.git = g
			listing, err := a.DiscoverCapabilityMarketplace(ctx, "https://github.com/anthropics/claude-plugins-official")
			if err != nil {
				t.Fatal(err)
			}
			if listing.Commit != commit || len(listing.Entries) != 2 || len(g.scans) != 1 || len(g.reads) != 1 {
				t.Fatalf("listing: %+v scans=%+v err=%v", listing, g.scans, err)
			}
			local, remote := listing.Entries[0], listing.Entries[1]
			if local.Commit != commit || local.Ref != commit || local.Path != "plugins/local" || remote.Trust != "unknown" || remote.Commit != "" || remote.Ref != "main" {
				t.Fatalf("unsafe sources %+v", listing)
			}
			records, err := a.caps().List(ctx)
			if err != nil || len(records) != 0 {
				t.Fatalf("discovery installed: %+v %v", records, err)
			}
			// The market branch changes before selection. The local source must remain
			// pinned to the discovery commit rather than scanning the newer branch.
			g.commit = strings.Repeat("b", 40)
			v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: local.Source, SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			proposal := v.(capabilityProposalView)
			if proposal.Lock.Source.Commit != commit || proposal.Lock.Source.Adapter != "claude-plugin" || g.scans[1].Ref != commit {
				t.Fatalf("unpinned proposal %+v", proposal)
			}
			if _, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: proposal.ID, SessionID: sid}); err == nil {
				t.Fatal("unknown source installed without review")
			}
			v, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: proposal.ID, SessionID: sid, Reviewed: true, SecondaryConfirm: true})
			if err != nil {
				t.Fatal(err)
			}
			record := v.(capability.Record)
			disabled := false
			for _, component := range record.Lock.Components {
				if component.Type == "hooks" {
					disabled = component.Disabled
				}
			}
			if !disabled || record.Lock.Source.Commit != commit {
				t.Fatalf("hooks enabled or pin lost: %+v", record.Lock)
			}
			if err = a.caps().Verify(ctx, record.ID, record.VersionID); err != nil {
				t.Fatal(err)
			}
			for _, read := range g.reads {
				if read.Commit != commit {
					t.Fatalf("moving ref read: %+v", read)
				}
			}
		})
	}
}
func TestMarketplaceURLNormalization(t *testing.T) {
	for _, test := range []struct{ raw, root, ref, manifest string }{
		{"https://github.com/org/market", ".", "", ""},
		{"https://github.com/org/market/tree/main", ".", "main", ""},
		{"https://github.com/org/market/tree/main/sub", "sub", "main", ""},
		{"https://github.com/org/market/tree/main/.claude-plugin", ".", "main", ".claude-plugin/marketplace.json"},
		{"https://github.com/org/market/blob/main/.claude-plugin/marketplace.json", ".", "main", ".claude-plugin/marketplace.json"},
		{"https://github.com/org/market/blob/main/sub/marketplace.json", "sub", "main", "marketplace.json"},
	} {
		clone, ref, root, manifest, err := marketplaceRepository(test.raw)
		if err != nil || clone != "https://github.com/org/market" || ref != test.ref || root != test.root || manifest != test.manifest {
			t.Fatalf("normalize %s: %s %s %s %s %v", test.raw, clone, ref, root, manifest, err)
		}
	}
	for _, raw := range []string{"https://github.com/org/repo/blob/main/evil.sh", "https://github.com/org/repo/tree/main/../bad", "https://github.com/org/repo/tree/--bad", "https://user:secret@github.com/org/repo", "file:///tmp/marketplace.json", "https://github.com/org/repo?token=secret", "https://github.com/org/repo/blob/main/%2e%2e/marketplace.json"} {
		if _, _, _, _, err := marketplaceRepository(raw); err == nil {
			t.Fatalf("accepted unsafe %s", raw)
		}
	}
}
func TestMarketplaceSourceCannotForgeTrustOrTraversal(t *testing.T) {
	for _, raw := range []string{
		`{"url":"file:///tmp/repo","root":"."}`,
		`{"url":"https://github.com/org/repo","root":"../escape"}`,
		`{"url":"https://github.com/org/repo","root":".","commit":"main"}`,
		`{"url":"https://github.com/org/repo","root":".","ref":"--upload-pack=evil"}`,
	} {
		source := capabilityMarketplacePrefix + base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, _, err := parseCapabilityMarketplaceSource(source); err == nil {
			t.Fatal("unsafe source accepted", raw)
		}
	}
	b, _ := json.Marshal(map[string]string{"url": "https://github.com/third-party/plugin", "root": ".", "trust": "official"})
	source, _, err := parseCapabilityMarketplaceSource(capabilityMarketplacePrefix + base64.RawURLEncoding.EncodeToString(b))
	if err != nil || source.Trust != "unknown" {
		t.Fatal(source, err)
	}
}

func TestMarketplaceRemoteSelectionResolvesThenPins(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	marketCommit := strings.Repeat("a", 40)
	pluginCommit := strings.Repeat("b", 40)
	g := &marketplaceFakeGit{commit: marketCommit, snapshots: map[string]map[string][]byte{
		".":      {"marketplace.json": []byte(`{"plugins":[{"name":"external","source":{"source":"url","url":"https://github.com/external/plugin/tree/release/plugin"}}]}`)},
		"plugin": {".claude-plugin/plugin.json": []byte(`{"name":"external"}`), "skills/demo/SKILL.md": []byte("---\nname: demo\ndescription: Demo\n---\nRead documents.\n")},
	}}
	a.capState.git = g
	listing, err := a.DiscoverCapabilityMarketplace(ctx, "https://github.com/anthropics/claude-plugins-official/blob/main/marketplace.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.scans) != 1 {
		t.Fatal("discovery fetched a remote entry")
	}
	entry := listing.Entries[0]
	if entry.Repository != "https://github.com/external/plugin" || entry.Ref != "release" || entry.Path != "plugin" || entry.Commit != "" || entry.Trust != "unknown" {
		t.Fatalf("bad remote source %+v", entry)
	}
	g.commit = pluginCommit
	v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: entry.Source, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	proposal := v.(capabilityProposalView)
	if proposal.Lock.Source.Commit != pluginCommit || proposal.Lock.Source.Trust != "unknown" {
		t.Fatalf("bad proposal %+v", proposal)
	}
	g.commit = strings.Repeat("c", 40)
	v, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: proposal.ID, SessionID: sid, Reviewed: true, SecondaryConfirm: true})
	if err != nil {
		t.Fatal(err)
	}
	record := v.(capability.Record)
	if record.Lock.Source.Commit != pluginCommit || g.reads[len(g.reads)-1].Commit != pluginCommit || len(g.scans) != 2 {
		t.Fatal("confirmation moved remote source")
	}
}

func TestMarketplaceMissingManifestAndMalformedEntries(t *testing.T) {
	for _, files := range []map[string][]byte{
		{"README.md": []byte("not a marketplace")},
		{"marketplace.json": []byte(`{"plugins":[{"name":"bad","source":"../escape"}]}`)},
		{"marketplace.json": []byte(`{"plugins":[{"name":"bad","source":{"source":"url","url":"file:///tmp/repo"}}]}`)},
		{"marketplace.json": []byte(`{"plugins":[{"name":"bad","source":{"source":"github","repo":"org/repo","sha":"main"}}]}`)},
	} {
		a := newTestApp(t)
		a.capState.git = &marketplaceFakeGit{commit: strings.Repeat("a", 40), snapshots: map[string]map[string][]byte{".": files}}
		if _, err := a.DiscoverCapabilityMarketplace(context.Background(), "https://github.com/org/market"); err == nil {
			t.Fatal("unsafe marketplace accepted")
		}
	}
}

func TestMarketplaceRediscoveryCreatesReviewedUpdate(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	original := strings.Repeat("a", 40)
	newer := strings.Repeat("b", 40)
	g := &marketplaceFakeGit{commit: original, snapshots: map[string]map[string][]byte{
		".":      {"marketplace.json": []byte(`{"plugins":[{"name":"local","source":"./plugin"}]}`)},
		"plugin": {".claude-plugin/plugin.json": []byte(`{"name":"local"}`), "skills/demo/SKILL.md": []byte("---\nname: demo\ndescription: Demo\n---\nFirst version.\n")},
	}}
	a.capState.git = g
	browse := func() capabilityMarketplaceEntry {
		t.Helper()
		listing, err := a.DiscoverCapabilityMarketplace(ctx, "https://github.com/org/market")
		if err != nil {
			t.Fatal(err)
		}
		return listing.Entries[0]
	}
	propose := func(entry capabilityMarketplaceEntry) capabilityProposalView {
		t.Helper()
		v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: entry.Source, SessionID: sid})
		if err != nil {
			t.Fatal(err)
		}
		return v.(capabilityProposalView)
	}
	confirm := func(p capabilityProposalView) capability.Record {
		t.Helper()
		v, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: p.ID, SessionID: sid, Reviewed: true, SecondaryConfirm: true})
		if err != nil {
			t.Fatal(err)
		}
		return v.(capability.Record)
	}
	installed := confirm(propose(browse()))
	g.commit = newer
	g.snapshots["plugin"]["skills/demo/SKILL.md"] = []byte("---\nname: demo\ndescription: Demo\n---\nSecond version.\n")
	entry := browse()
	pending := propose(entry)
	if pending.Kind != "update" || pending.Status != "pending" || pending.BaseVersionID != installed.VersionID || pending.Lock.ID != installed.ID || pending.Lock.Source.Commit != newer || pending.Diff == nil || len(pending.Diff.Changed) != 1 {
		t.Fatalf("bad update proposal: %+v", pending)
	}
	current, err := a.caps().Get(ctx, installed.ID)
	if err != nil || current.VersionID != installed.VersionID {
		t.Fatal("discovery/proposal auto-adopted update", current, err)
	}
	if _, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{ProposalID: pending.ID, SessionID: sid}); err == nil {
		t.Fatal("update bypassed review")
	}
	g.commit = strings.Repeat("c", 40)
	adopted := confirm(pending)
	if adopted.ID != installed.ID || adopted.VersionID == installed.VersionID || adopted.Lock.Source.Commit != newer {
		t.Fatalf("wrong adopted version: %+v", adopted)
	}
	if _, err = a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{Source: entry.Source, SessionID: sid}); err == nil {
		t.Fatal("duplicate snapshot proposed as update")
	}
	for _, changed := range []string{"url", "path", "adapter", "kind"} {
		candidate := pending.Lock
		switch changed {
		case "url":
			candidate.Source.URL = "https://github.com/attacker/replacement"
		case "path":
			candidate.Source.Subpaths = []string{"different-plugin"}
		case "adapter":
			candidate.Source.Adapter = "tidebot-native"
		case "kind":
			candidate.Source.Kind = "registry"
		}
		// Deliberately force the same stable ID: identity checks must still reject.
		if _, err = a.proposeMarketplaceUpdate(ctx, sid, adopted, candidate); err == nil {
			t.Fatal("identity collision accepted", changed)
		}
	}
}
