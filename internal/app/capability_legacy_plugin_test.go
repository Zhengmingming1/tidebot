package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"strings"
	"testing"
)

func TestCapabilityLegacyPluginReviewRemainsInert(t *testing.T) {
	a, _, sid := capFixture(t)
	ctx := context.Background()
	a.capState.git = &marketplaceFakeGit{commit: strings.Repeat("a", 40), snapshots: map[string]map[string][]byte{".": {"plugin.yaml": []byte("id: demo\nname: Demo\nversion: 1.0.0\nruntime:\n  command: bin/demo\ncontributes:\n  connectors:\n    - id: remote\n      mcp:\n        command: bin/remote\n")}}}
	v, err := a.CapabilityRequest(ctx, "propose", webui.CapabilityRequest{SessionID: sid, Source: "https://github.com/example/plugin"})
	if err != nil {
		t.Fatal(err)
	}
	p := v.(capabilityProposalView)
	if p.Lock.Source.Adapter != "tidebot-plugin" || len(p.Lock.Components) != 2 {
		t.Fatalf("unexpected review %+v", p)
	}
	for _, c := range p.Lock.Components {
		if !c.Disabled || c.MCP != nil {
			t.Fatal("legacy execution imported")
		}
	}
	if _, err = a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID, Reviewed: true, SecondaryConfirm: true}); err != nil {
		t.Fatal(err)
	}
	records, err := a.caps().List(ctx)
	if err != nil || len(records) != 1 {
		t.Fatalf("records %v %v", records, err)
	}
	if records[0].Lock.Source.Adapter != "tidebot-plugin" {
		t.Fatal("adapter lost")
	}
	var count int
	if err = a.DB.QueryRowContext(ctx, "SELECT count(*) FROM plugins").Scan(&count); err != nil || count != 0 {
		t.Fatalf("native plugin installed without permission: %d %v", count, err)
	}
	if err = records[0].Lock.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := records[0].Lock
	bad.Components = append([]capability.Component(nil), bad.Components...)
	bad.Components[0].Disabled = false
	if bad.Validate() == nil {
		t.Fatal("active native plugin component accepted")
	}
}
