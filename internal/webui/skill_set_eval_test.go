package webui

import (
	"context"
	"net/http"
	"testing"
)

func TestSkillSetEvaluationEndpoints(t *testing.T) {
	c := login(t)
	var received SkillSetEvalRequest
	var canceled string
	c.s.deps.StartSkillSetEval = func(req SkillSetEvalRequest) (SkillSetEvaluation, error) {
		received = req
		return SkillSetEvaluation{ID: "eval_test", SetID: req.SetID, VersionID: req.VersionID, Status: "running"}, nil
	}
	c.s.deps.GetSkillSetEval = func(ctx context.Context, id string) (SkillSetEvaluation, error) {
		return SkillSetEvaluation{ID: id, Status: "completed", Cases: []EvalCaseResult{{Name: "sample", Baseline: &EvalRunResult{Status: "succeeded", Passed: false}, Composition: &EvalRunResult{Status: "succeeded", Passed: true}}}}, nil
	}
	c.s.deps.ListSkillSetEvals = func(ctx context.Context, id string) ([]SkillSetEvaluation, error) {
		return []SkillSetEvaluation{{ID: "eval_test", SetID: id}}, nil
	}
	c.s.deps.CancelSkillSetEval = func(id string) error { canceled = id; return nil }
	code, out := c.json(http.MethodPost, "/api/skill-set-evals", map[string]any{"set_id": "set_test", "version_id": "ver_test", "provider": "test", "budget_usd": 0.2})
	if code != 202 || out["id"] != "eval_test" || received.BudgetUSD != 0.2 || received.VersionID != "ver_test" {
		t.Fatal(code, out, received)
	}
	code, out = c.json(http.MethodGet, "/api/skill-set-evals/eval_test", nil)
	if code != 200 || out["status"] != "completed" || len(out["cases"].([]any)) != 1 {
		t.Fatal(code, out)
	}
	if rr := c.do(http.MethodGet, "/api/skill-set-evals?set_id=set_test", "", nil); rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	if code, _ = c.json(http.MethodPost, "/api/skill-set-evals/eval_test/cancel", nil); code != 200 || canceled != "eval_test" {
		t.Fatal(code, canceled)
	}
	if code, _ = c.json(http.MethodPost, "/api/skill-set-evals", map[string]any{"set_id": "set_test", "budget_usd": 0.2}); code != 400 {
		t.Fatal("unpinned evaluation accepted", code)
	}
}
