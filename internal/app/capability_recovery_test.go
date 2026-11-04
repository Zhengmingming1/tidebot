package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/optimize"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func countRecoveryRuns(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecoverRunCreatesOnlyDraft(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	rid := originRun(t, a, runner.Options{Prompt: "missing tool"})
	if _, err := a.CapabilityRequest(ctx, "recover", webui.CapabilityRequest{RunID: rid}); err == nil {
		t.Fatal("successful run accepted")
	}
	if _, err := a.DB.Exec(`UPDATE runs SET status='failed',error_message='command not found: missing-tool' WHERE id=?`, rid); err != nil {
		t.Fatal(err)
	}
	before := countRecoveryRuns(t, a)
	out, err := a.CapabilityRequest(ctx, "recover", webui.CapabilityRequest{RunID: rid})
	if err != nil {
		t.Fatal(err)
	}
	got := out.(webui.CapabilityRecovery)
	if got.Session.Origin != "run_fix" || got.Session.Turns != 0 || got.Session.Status != "idle" || got.DraftPrompt == "" {
		t.Fatalf("%+v", got)
	}
	again, err := a.CapabilityRequest(ctx, "recover", webui.CapabilityRequest{RunID: rid})
	if err != nil || again.(webui.CapabilityRecovery).Session.ID != got.Session.ID {
		t.Fatalf("repeat: %v %+v", err, again)
	}
	if countRecoveryRuns(t, a) != before {
		t.Fatal("draft started a paid run")
	}
}

func optimizationFixture(t *testing.T) (*App, string, string, string) {
	t.Helper()
	a, _, sid := capFixture(t)
	dir := t.TempDir()
	md := "---\nname: recovery-test\ndescription: Recovery test skill\n---\n\nRead the document.\nCheck the facts.\nReturn a concise answer.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0600); err != nil {
		t.Fatal(err)
	}
	skill, err := a.Skills.ImportDir(context.Background(), "recovery-test", dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	return a, sid, skill.ID, md
}

func TestSessionOptimizationRequiresOwnerAndDoesNotPromote(t *testing.T) {
	a, sid, skillID, md := optimizationFixture(t)
	ctx := context.Background()
	if err := a.Optimize.SetSettings(ctx, skillID, optimize.Settings{Mode: "canary", Ratio: 1, Runs: 5}); err != nil {
		t.Fatal(err)
	}
	current, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	before := countRecoveryRuns(t, a)
	out, err := a.CapabilityRequest(ctx, "skill-optimize", webui.CapabilityRequest{SessionID: sid, SkillID: skillID, Reason: "Make failure checks clearer"})
	if err != nil {
		t.Fatal(err)
	}
	p := out.(capabilityProposalView)
	stored, err := a.caps().GetProposal(ctx, p.ID)
	if err != nil || stored.SkillOptimization == nil || stored.SkillOptimization.Instruction != "Make failure checks clearer" {
		t.Fatalf("persisted: %v %+v", err, stored)
	}
	if p.CostCapUSD != .10 || p.Kind != "skill_optimize" || countRecoveryRuns(t, a) != before {
		t.Fatal("proposal must be free and advertise cap")
	}
	other, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: other.ID, ProposalID: p.ID}); err == nil {
		t.Fatal("cross-session confirmation accepted")
	}
	analysis, _ := json.Marshal(map[string]any{"category": "robustness", "rationale": "Clarify checks", "files": map[string]string{"SKILL.md": md + "Explain missing dependencies before continuing.\n"}})
	t.Setenv("TIDEBOT_MOCK_RESULT_TEXT", string(analysis))
	confirmed, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.(map[string]any)["optimization_proposal_id"] == "" {
		t.Fatal("missing review result")
	}
	if countRecoveryRuns(t, a) != before+1 {
		t.Fatal("expected one explicit analyzer run")
	}
	proposals, err := a.Optimize.Proposals(ctx, skillID)
	if err != nil || len(proposals) != 1 || proposals[0].Status == "canary" || proposals[0].Source != "user" {
		t.Fatalf("unexpected optimizer result %v %+v", err, proposals)
	}
	after, _ := a.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	if after.ID != current.ID {
		t.Fatal("optimization promoted current version")
	}
	if _, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID}); err == nil {
		t.Fatal("duplicate confirm accepted")
	}
	if countRecoveryRuns(t, a) != before+1 {
		t.Fatal("duplicate confirmation started paid run")
	}
}

func TestModelOptimizationIsBoundAndProposalOnly(t *testing.T) {
	a, sid, skillID, _ := optimizationFixture(t)
	ctx := context.Background()
	a.capState.mu.Lock()
	a.capState.tokens = map[string]capabilityToken{}
	a.capState.tokens["test-recovery-token"] = capabilityToken{SessionID: sid, Expires: time.Now().Add(time.Hour)}
	a.capState.mu.Unlock()
	call := func(action string) error {
		raw, _ := json.Marshal(map[string]any{"token": "test-recovery-token", "tool": "cap_propose", "args": map[string]string{"action": action, "skill_id": skillID, "reason": "Improve error handling"}})
		_, err := a.rpcSessionTool(ctx, raw)
		return err
	}
	before := countRecoveryRuns(t, a)
	if err := call("skill_optimize"); err == nil {
		t.Fatal("unbound skill accepted")
	}
	if _, err := a.SetChatBindings(ctx, sid, webui.ChatBindingsInput{Skills: []string{skillID}}); err != nil {
		t.Fatal(err)
	}
	if err := call("skill_optimize"); err != nil {
		t.Fatal(err)
	}
	if err := call("confirm"); err == nil {
		t.Fatal("model confirmation accepted")
	}
	if countRecoveryRuns(t, a) != before {
		t.Fatal("model started optimization")
	}
	proposals, err := a.caps().ListProposals(ctx, sid)
	if err != nil || len(proposals) != 1 || !strings.Contains(proposals[0].Kind, "skill_optimize") {
		t.Fatalf("%v %+v", err, proposals)
	}
}

func TestSessionOptimizationBusyStaleAndDismissAreFree(t *testing.T) {
	a, sid, skillID, md := optimizationFixture(t)
	ctx := context.Background()
	before := countRecoveryRuns(t, a)
	out, err := a.CapabilityRequest(ctx, "skill-optimize", webui.CapabilityRequest{SessionID: sid, SkillID: skillID, Reason: "Clarify checks"})
	if err != nil {
		t.Fatal(err)
	}
	p := out.(capabilityProposalView)
	if _, err := a.DB.Exec(`UPDATE chat_sessions SET status='running' WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID}); err == nil {
		t.Fatal("busy session accepted")
	}
	if _, err := a.DB.Exec(`UPDATE chat_sessions SET status='idle' WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Skills.CommitFiles(ctx, skillID, "recovery-test", map[string][]byte{"SKILL.md": []byte(md + "Owner edit.\n")}, "edit", "user", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID}); err == nil {
		t.Fatal("stale version accepted")
	}
	if _, err := a.CapabilityRequest(ctx, "dismiss", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CapabilityRequest(ctx, "confirm", webui.CapabilityRequest{SessionID: sid, ProposalID: p.ID}); err == nil {
		t.Fatal("dismissed proposal accepted")
	}
	if countRecoveryRuns(t, a) != before {
		t.Fatal("failed or dismissed confirmation started a run")
	}
}

func TestRecoveryConcurrentClicksReuseOneSession(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	rid := originRun(t, a, runner.Options{Prompt: "failed dependency"})
	if _, err := a.DB.Exec(`UPDATE runs SET status='failed' WHERE id=?`, rid); err != nil {
		t.Fatal(err)
	}
	results := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			out, err := a.CapabilityRequest(ctx, "recover", webui.CapabilityRequest{RunID: rid})
			if err != nil {
				errs <- err
				results <- ""
				return
			}
			results <- out.(webui.CapabilityRecovery).Session.ID
		}()
	}
	first := ""
	for i := 0; i < 8; i++ {
		got := <-results
		if got == "" {
			t.Fatal(<-errs)
		}
		if first == "" {
			first = got
		}
		if got != first {
			t.Fatal("recovery created duplicate sessions")
		}
	}
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM chat_sessions WHERE origin_run_id=?`, rid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sessions %d: %v", n, err)
	}
}
