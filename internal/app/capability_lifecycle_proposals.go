package app

import (
	"context"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// proposeCapabilityLifecycle is reachable by model tools and only drafts cards.
// In particular, never route its action to the UI's executing uninstall action.
func (a *App) proposeCapabilityLifecycle(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	if _, err := a.chatSession(ctx, r.SessionID); err != nil {
		return nil, err
	}
	if r.CapabilityID == "" {
		r.CapabilityID = r.Source
	}
	if action == "update" {
		return a.capabilityLifecycleRequest(ctx, "update", r)
	}
	if action != "uninstall" {
		return nil, capError(400, "unsupported", "unsupported lifecycle proposal")
	}
	current, err := a.caps().Get(ctx, r.CapabilityID)
	if err != nil {
		return nil, err
	}
	preview, err := a.caps().PreviewUninstall(ctx, current.ID, current.VersionID)
	if errors.Is(err, capability.ErrReferenced) {
		return nil, capError(409, "referenced", "owner must explicitly remove bindings or deactivate this capability before proposing uninstall")
	}
	if err != nil {
		return nil, err
	}
	p, err := a.caps().CreateProposal(ctx, capability.Proposal{SessionID: r.SessionID, Kind: "uninstall", Lock: current.Lock, BaseVersionID: current.VersionID, UninstallPreview: &preview})
	return proposalView(p), err
}

// decideCapabilityUninstall is owner UI only. Exact reviewed version and session
// ownership are required even when invoked outside the normal request router.
func (a *App) decideCapabilityUninstall(ctx context.Context, action string, r webui.CapabilityRequest, p capability.Proposal) (any, error) {
	if p.Kind != "uninstall" || p.SessionID != r.SessionID {
		return nil, capError(404, "not_found", "proposal not found")
	}
	if action != "confirm" && action != "dismiss" {
		return nil, capError(400, "unsupported", "uninstall reviews the deletion summary, not upstream files")
	}
	if action == "confirm" {
		if p.UninstallPreview == nil || !r.Reviewed || r.VersionID == "" || r.VersionID != p.BaseVersionID {
			return nil, capError(400, "review_required", "review the deletion summary and confirm the exact installed version")
		}
		if _, err := a.caps().PreviewUninstall(ctx, p.Lock.ID, p.BaseVersionID); err != nil {
			return nil, capError(409, "uninstall_changed", err.Error())
		}
	}
	p, err := a.caps().ClaimProposal(ctx, p.ID, r.SessionID, "ui")
	if err != nil {
		return nil, capError(409, "proposal_unavailable", err.Error())
	}
	if action == "dismiss" {
		err = a.caps().FinishProposal(ctx, p.ID, "ui", "dismissed")
		return map[string]bool{"ok": err == nil}, err
	}
	result, err := a.caps().UninstallVersionWithReport(ctx, p.Lock.ID, p.BaseVersionID, "ui", *p.UninstallPreview)
	status := "applied"
	if err != nil {
		status = "failed"
	}
	finishErr := a.caps().FinishProposal(context.Background(), p.ID, "ui", status)
	if err != nil {
		return nil, err
	}
	return result, finishErr
}
