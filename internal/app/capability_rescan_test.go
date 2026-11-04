package app

import (
	"context"
	"encoding/json"
	"github.com/Zhengmingming1/tidebot/internal/webui"
	"testing"
)

func TestCapabilityRescanRequiresNewOwnerConfirmation(t *testing.T) {
	a, r := installedCap(t)
	ctx := context.Background()
	session, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := a.proposeCapabilityRescan(ctx, webui.CapabilityRequest{CapabilityID: r.ID, SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	var p capabilityProposalView
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Kind != "update" || p.BaseVersionID != r.VersionID || p.Lock.FilesSHA256 != r.Lock.FilesSHA256 {
		t.Fatal(p)
	}
	after, err := a.caps().Get(ctx, r.ID)
	if err != nil || after.VersionID != r.VersionID {
		t.Fatal("rescan silently adopted", err)
	}
}
