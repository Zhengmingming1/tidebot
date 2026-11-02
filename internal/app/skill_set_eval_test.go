package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func evaluationFixture(t *testing.T, a *App, environment []skillstore.RuntimeRequirement) skillstore.SkillSet {
	t.Helper()
	ctx := context.Background()
	helper := importPreflightSkill(t, a, "eval-helper", "Offer supporting guidance.", skillstore.SkillManifest{})
	main := importPreflightSkill(t, a, "eval-main", "Return a concise answer.", skillstore.SkillManifest{Format: skillstore.SkillManifestFormat, Dependencies: []skillstore.SkillDependency{{SkillName: "eval-helper"}}, Environment: environment})
	details := skillstore.SkillSetDetails{Description: "Mock acceptance examples", TestCases: []skillstore.EvalCase{{Name: "case", Input: "sample input", MustContain: []string{"expected"}, MustNotContain: []string{"forbidden"}}}}
	set, err := a.Skills.SaveSetWithDetails(ctx, "", "Eval package", "", []tasksvc.SkillBinding{{SkillID: main, Role: "primary", Mode: "follow"}, {SkillID: helper, Role: "auxiliary", Mode: "follow"}}, &details)
	if err != nil {
		t.Fatal(err)
	}
	return set
}
func waitEvaluation(t *testing.T, a *App, eid string) webui.SkillSetEvaluation {
	t.Helper()
	var got webui.SkillSetEvaluation
	waitFor(t, 10*time.Second, func() bool {
		var err error
		got, err = a.getSkillSetEval(context.Background(), eid)
		return err == nil && got.Status != "running"
	})
	return got
}

func TestSkillSetEvaluationMockComparisonAndIsolation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	set := evaluationFixture(t, a, nil)
	t.Setenv("TIDEBOT_MOCK_RESULT_TEXT", "expected answer")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	ev, err := a.startSkillSetEval(webui.SkillSetEvalRequest{SetID: set.ID, VersionID: set.VersionID, BudgetUSD: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	got := waitEvaluation(t, a, ev.ID)
	if got.Status != "completed" || len(got.Cases) != 1 || got.Provider == "" {
		t.Fatalf("%+v", got)
	}
	b, c := got.Cases[0].Baseline, got.Cases[0].Composition
	if b == nil || c == nil || !b.Passed || !c.Passed || b.RunID == c.RunID || len(b.Assertions) != 2 || b.InputTokens == 0 || c.OutputTokens == 0 {
		t.Fatalf("%+v %+v", b, c)
	}
	bbs, _ := a.Skills.RunBindings(ctx, b.RunID)
	cbs, _ := a.Skills.RunBindings(ctx, c.RunID)
	if len(bbs) != 1 || len(cbs) != 2 || bbs[0].VersionID != set.SkillBindings[0].VersionID || cbs[1].VersionID != set.SkillBindings[1].VersionID {
		t.Fatal("comparison did not use frozen members", bbs, cbs)
	}
	if _, err = a.stageSkills(ctx, bbs); err == nil {
		t.Fatal("baseline exception leaked into normal task staging")
	}
	argsRaw, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(argsRaw)), "\n")
	if argAfter(args, "--tools") != "Read,Glob,Grep" || argAfter(args, "--allowedTools") != "Read,Glob,Grep" || argAfter(args, "--mcp-config") != `{"mcpServers":{}}` || !contains(args, "--strict-mcp-config") || !strings.Contains(argAfter(args, "--disallowedTools"), "Bash") {
		t.Fatalf("unsafe arguments: %s", argsRaw)
	}
	for _, r := range []*webui.EvalRunResult{b, c} {
		var prompt, bucket, trigger, mode string
		if err := a.DB.QueryRow(`SELECT prompt,cost_bucket,trigger_kind,run_mode FROM runs WHERE id=?`, r.RunID).Scan(&prompt, &bucket, &trigger, &mode); err != nil {
			t.Fatal(err)
		}
		if prompt != "sample input" || bucket != "assist" || trigger != "skill_set_eval" || mode != "read" {
			t.Fatal(prompt, bucket, trigger, mode)
		}
	}
	list, err := a.listSkillSetEvals(ctx, set.ID)
	if err != nil || len(list) != 1 || list[0].ID != got.ID {
		t.Fatal(list, err)
	}
}

func TestEvaluationPreflightBlocksBeforeAnyRun(t *testing.T) {
	for _, req := range []skillstore.RuntimeRequirement{{Kind: "command", Name: "sh"}, {Kind: "python_package", Name: "requests"}, {Kind: "mcp", Name: "github"}, {Kind: "tool", Name: "WebFetch"}, {Kind: "file", Name: "absent.txt"}} {
		t.Run(req.Kind, func(t *testing.T) {
			a := newTestApp(t)
			set := evaluationFixture(t, a, []skillstore.RuntimeRequirement{req})
			if _, err := a.startSkillSetEval(webui.SkillSetEvalRequest{SetID: set.ID, VersionID: set.VersionID, BudgetUSD: 0.1}); err == nil {
				t.Fatal("required blocked dependency started")
			}
			var count int
			_ = a.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&count)
			if count != 0 {
				t.Fatal("preflight launched model", count)
			}
		})
	}
	a := newTestApp(t)
	set := evaluationFixture(t, a, []skillstore.RuntimeRequirement{{Kind: "mcp", Name: "github", Optional: true}})
	if _, err := a.evaluationPreflight(context.Background(), set, ""); err != nil {
		t.Fatal("optional dependency blocked evaluation", err)
	}
	for _, budget := range []float64{0, 0.01, 5.01} {
		if _, err := a.startSkillSetEval(webui.SkillSetEvalRequest{SetID: set.ID, VersionID: set.VersionID, BudgetUSD: budget}); err == nil {
			t.Fatal("budget accepted", budget)
		}
	}
}

func TestSkillSetEvaluationCancellation(t *testing.T) {
	a := newTestApp(t)
	set := evaluationFixture(t, a, nil)
	t.Setenv("TIDEBOT_MOCK_STEPS", "20")
	t.Setenv("TIDEBOT_MOCK_STEP_SLEEP", "0.1")
	req := webui.SkillSetEvalRequest{SetID: set.ID, VersionID: set.VersionID, BudgetUSD: 0.2}
	ev, err := a.startSkillSetEval(req)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		got, _ := a.getSkillSetEval(context.Background(), ev.ID)
		return got.Cases[0].Baseline != nil && got.Cases[0].Baseline.RunID != ""
	})
	if _, err := a.startSkillSetEval(req); err == nil {
		t.Fatal("overlapping evaluation accepted")
	}
	if err = a.cancelSkillSetEval(ev.ID); err != nil {
		t.Fatal(err)
	}
	got := waitEvaluation(t, a, ev.ID)
	if got.Status != "canceled" || got.Cases[0].Composition != nil || got.FinishedAt == "" {
		t.Fatalf("%+v", got)
	}
}

func TestSkillSetEvaluationInterruptedRecoveryDoesNotRerun(t *testing.T) {
	a := newTestApp(t)
	set := evaluationFixture(t, a, nil)
	ev := webui.SkillSetEvaluation{ID: "eval_interrupted", SetID: set.ID, VersionID: set.VersionID, Status: "running", CreatedAt: nowUTC()}
	raw, _ := json.Marshal(ev)
	_, err := a.DB.Exec(`INSERT INTO skill_set_evaluations(id,set_id,version_id,status,state_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, ev.ID, set.ID, set.VersionID, "running", string(raw), nowUTC(), nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.initSkillSetEvals(); err != nil {
		t.Fatal(err)
	}
	got, err := a.getSkillSetEval(context.Background(), ev.ID)
	if err != nil || got.Status != "interrupted" || got.FinishedAt == "" {
		t.Fatal(got, err)
	}
	var count int
	_ = a.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&count)
	if count != 0 {
		t.Fatal("recovery restarted paid work")
	}
}

func TestEvaluationAssertionsNeverTreatEmptyOrFailedRunAsPassing(t *testing.T) {
	res := runner.Result{Status: "succeeded", ResultText: "expected"}
	if evaluateTextResult(res, skillstore.EvalCase{}, 0, nil).Passed {
		t.Fatal("empty assertions passed")
	}
	if evaluateTextResult(res, skillstore.EvalCase{MustContain: []string{""}}, 0, nil).Passed {
		t.Fatal("blank assertion passed")
	}
	c := skillstore.EvalCase{MustContain: []string{"expected"}, MustNotContain: []string{"forbidden"}}
	if !evaluateTextResult(res, c, 0, nil).Passed {
		t.Fatal("valid text failed")
	}
	res.Status = "failed"
	if evaluateTextResult(res, c, 0, nil).Passed {
		t.Fatal("failed run passed")
	}
	res.Status = "succeeded"
	res.ResultText = strings.Repeat("x", 70<<10) + "expected"
	out := evaluateTextResult(res, c, 0, nil)
	if !out.Passed || !out.OutputTruncated || len(out.Output) > 64<<10 {
		t.Fatal("assertion must inspect complete output before display clipping")
	}
}

func TestSkillSetEvaluationPreflightUsesSelectedProvider(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		defaultConfigured, selectedConfigured bool
	}{
		{name: "selected_provider_configured", selectedConfigured: true},
		{name: "only_default_provider_configured", defaultConfigured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			const envName = "TIDEBOT_EVALUATION_PROVIDER_REQUIREMENT"
			set := evaluationFixture(t, a, []skillstore.RuntimeRequirement{{Kind: "env", Name: envName}})
			fallback := mustProfile(t, a, a.Cfg.DefaultProvider)
			fallback.ExtraEnv = map[string]string{}
			if tc.defaultConfigured {
				fallback.ExtraEnv[envName] = "default-fixture"
			}
			if _, err := a.Registry.Put(fallback); err != nil {
				t.Fatal(err)
			}
			selected := fallback
			selected.Name = "evaluation-selected"
			selected.ExtraEnv = map[string]string{}
			if tc.selectedConfigured {
				selected.ExtraEnv[envName] = "selected-fixture"
			}
			if _, err := a.Registry.Put(selected); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TIDEBOT_MOCK_RESULT_TEXT", "expected answer")
			ev, err := a.startSkillSetEval(webui.SkillSetEvalRequest{SetID: set.ID, VersionID: set.VersionID, Provider: selected.Name, BudgetUSD: 0.2})
			if !tc.selectedConfigured {
				if err == nil {
					t.Fatal("default provider environment incorrectly satisfied selected provider requirement")
				}
				var count int
				_ = a.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&count)
				if count != 0 {
					t.Fatal("incorrect provider preflight launched a run", count)
				}
				return
			}
			if err != nil {
				t.Fatal("selected provider environment was ignored", err)
			}
			got := waitEvaluation(t, a, ev.ID)
			if got.Status != "completed" || got.Preflight == nil || got.Preflight.RuntimeProfile.Provider != selected.Name {
				t.Fatalf("wrong provider comparison: %+v", got)
			}
			raw, _ := json.Marshal(got.Preflight)
			if strings.Contains(string(raw), "selected-fixture") {
				t.Fatal("preflight leaked environment value")
			}
		})
	}
}
