package app

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A rescan is a new reviewable version, never an in-place edit of immutable locks.
func (a *App) proposeCapabilityRescan(ctx context.Context, r webui.CapabilityRequest) (any, error) {
	if _, err := a.chatSession(ctx, r.SessionID); err != nil {
		return nil, err
	}
	record, err := a.caps().Get(ctx, r.CapabilityID)
	if err != nil {
		return nil, err
	}
	raw, err := a.caps().ExportBundle(ctx, record.ID, record.VersionID)
	if err != nil {
		return nil, err
	}
	_, files, err := capability.ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	lock := record.Lock
	lock.Scan, err = scanCapabilitySource(lock.Source, files)
	if err != nil {
		return nil, err
	}
	if err = a.resolveCapabilityDependencies(ctx, &lock, true); err != nil {
		return nil, err
	}
	p, diff, err := a.caps().CreateUpdateProposal(ctx, r.SessionID, "", lock, files)
	if err != nil {
		return nil, err
	}
	return struct {
		capabilityProposalView
		Diff capability.Diff `json:"diff"`
	}{proposalView(p), diff}, nil
}
