package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// recoverRunInSession prepares an owner-editable request, never a paid turn.
// The actual run record is already carried into the session as untrusted data.
func (a *App) recoverRunInSession(ctx context.Context, runID string) (any, error) {
	var status string
	err := a.DB.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, strings.TrimSpace(runID)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, webui.ErrChatRunNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "failed" && status != "timeout" {
		return nil, capError(409, "not_failed", "only failed or timed-out runs can be recovered")
	}
	session, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: runID})
	if err != nil {
		return nil, err
	}
	if session.Status != "idle" {
		return nil, webui.ErrChatSessionBusy
	}
	// Do not relabel an existing active conversation or a different origin run.
	if session.Turns == 0 && session.OriginRunID == strings.TrimSpace(runID) {
		if _, err = a.DB.ExecContext(ctx, `UPDATE chat_sessions SET origin='run_fix' WHERE id=? AND turns=0 AND status='idle'`, session.ID); err != nil {
			return nil, err
		}
		session.Origin = "run_fix"
	}
	return webui.CapabilityRecovery{Session: session, DraftPrompt: "Please diagnose this failed run. Check whether a missing tool, runtime, credential, or capability caused the failure. Explain the evidence and propose a fix for my review. Do not install anything, change permissions, or rerun the task automatically."}, nil
}

func (a *App) sessionHasSkill(ctx context.Context, sessionID, skillID string) bool {
	if skillID == "" {
		return false
	}
	row, err := a.chatSession(ctx, sessionID)
	if err != nil {
		return false
	}
	bindings := parseChatBindings(row.bindingsRaw)
	for _, id := range bindings.Skills {
		if id == skillID {
			return true
		}
	}
	for _, id := range bindings.Capabilities {
		record, err := a.caps().Get(ctx, id)
		if err != nil {
			continue
		}
		for _, c := range record.Lock.Components {
			if !c.Disabled && c.SkillID == skillID {
				return true
			}
		}
	}
	return false
}

func (a *App) proposeSessionOptimization(ctx context.Context, r webui.CapabilityRequest) (any, error) {
	row, err := a.chatSession(ctx, r.SessionID)
	if err != nil {
		return nil, err
	}
	if row.Status == "closed" {
		return nil, webui.ErrChatInvalid
	}
	instruction := strings.TrimSpace(r.Reason)
	if r.SkillID == "" || instruction == "" || len(instruction) > webui.ChatMessageMax {
		return nil, capError(400, "invalid", "skill_id and a bounded optimization reason are required")
	}
	cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, r.SkillID)
	if err != nil {
		return nil, capError(404, "skill_not_found", "skill not found")
	}
	p, err := a.caps().CreateProposal(ctx, capability.Proposal{
		SessionID: r.SessionID, Kind: "skill_optimize",
		SkillOptimization: &capability.SkillOptimization{SkillID: r.SkillID, VersionID: cur.ID, Instruction: instruction},
		Lock:              capability.Lock{Title: a.skillName(r.SkillID), Components: []capability.Component{{Type: "skill", SkillID: r.SkillID, VersionID: cur.ID}}},
	})
	return proposalView(p), err
}

// This entry point is called only from the authenticated owner decision route.
// Models can create a proposal but never confirm it or invoke AnalyzeSkill.
func (a *App) decideSessionOptimization(ctx context.Context, action string, r webui.CapabilityRequest, p capability.Proposal) (any, error) {
	if action != "confirm" && action != "dismiss" {
		return nil, capError(400, "unsupported", "review optimization changes on the skill page after analysis")
	}
	if action == "confirm" {
		if p.SkillOptimization == nil {
			return nil, capError(400, "invalid", "optimization request missing")
		}
		row, err := a.chatSession(ctx, r.SessionID)
		if err != nil {
			return nil, err
		}
		if row.Status != "idle" {
			return nil, webui.ErrChatSessionBusy
		}
		cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, p.SkillOptimization.SkillID)
		if err != nil || cur.ID != p.SkillOptimization.VersionID {
			return nil, capError(409, "skill_changed", "skill changed; create a new proposal")
		}
	}
	if action == "confirm" {
		// Match the turn submission CAS: one session cannot start a turn and an
		// optimizer concurrently, and deleting a session cannot orphan the analysis.
		res, err := a.DB.ExecContext(ctx, `UPDATE chat_sessions SET status='running' WHERE id=? AND status='idle'`, r.SessionID)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, webui.ErrChatSessionBusy
		}
		defer func() {
			_, _ = a.DB.ExecContext(context.Background(), `UPDATE chat_sessions SET status='idle' WHERE id=? AND status='running'`, r.SessionID)
		}()
	}
	claimed, err := a.caps().ClaimProposal(ctx, p.ID, r.SessionID, "ui")
	if err != nil {
		return nil, capError(409, "proposal_unavailable", err.Error())
	}
	if action == "dismiss" {
		err = a.caps().FinishProposal(ctx, p.ID, "ui", "dismissed")
		return map[string]bool{"ok": err == nil}, err
	}
	request := claimed.SkillOptimization
	// Non-empty instruction makes the existing optimizer source=user, preventing
	// automatic canary enrollment even if this skill enables auto canary mode.
	result, err := a.AnalyzeSkill(ctx, request.SkillID, request.Instruction)
	if err != nil {
		_ = a.caps().FinishProposal(context.Background(), p.ID, "ui", "failed")
		return nil, err
	}
	if err = a.caps().FinishProposal(ctx, p.ID, "ui", "applied"); err != nil {
		return nil, err
	}
	return map[string]any{"skill_id": request.SkillID, "optimization_proposal_id": result.ID, "navigation_path": "#/skills/" + request.SkillID}, nil
}
