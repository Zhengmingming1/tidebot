package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func TestContextPlanAdmissionAndPrivateAudit(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	promptFixture := "private-prompt-sentinel-8671"
	res, err := a.RunPromptEx(ctx, runner.Options{Prompt: promptFixture}, "kimi-coding", 0)
	if err != nil || res.Status != "succeeded" {
		t.Fatal(res, err)
	}
	var raw string
	if err = a.DB.QueryRow(`SELECT detail_json FROM operation_log WHERE entity_type='run' AND entity_id=? AND action='context.plan'`, res.RunID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, promptFixture) {
		t.Fatal("prompt leaked into audit")
	}
	var plan contextplan.Plan
	if err = json.Unmarshal([]byte(raw), &plan); err != nil || plan.ID == "" || plan.EstimatedTokens <= 0 || plan.Provider != "kimi-coding" {
		t.Fatal(raw, err)
	}
	if _, err = a.DB.Exec(`UPDATE operation_log SET detail_json='{}' WHERE entity_id=?`, res.RunID); err == nil {
		t.Fatal("audit is mutable")
	}
}

func TestContextBudgetAndAuditFailurePreventProcess(t *testing.T) {
	for _, which := range []string{"budget", "audit"} {
		t.Run(which, func(t *testing.T) {
			a := newTestApp(t)
			args := filepath.Join(t.TempDir(), "args")
			t.Setenv("TIDEBOT_MOCK_ARGS_FILE", args)
			o := runner.Options{Prompt: strings.Repeat("required", 100)}
			if which == "budget" {
				o.ContextRequest = &contextplan.Request{Scope: "execution", Budget: contextplan.Budget{MaxInputTokens: 20, SystemReserve: 1}}
			} else {
				if _, err := a.DB.Exec(`CREATE TRIGGER reject_context_audit BEFORE INSERT ON operation_log WHEN NEW.action='context.plan' BEGIN SELECT RAISE(FAIL,'audit fault'); END`); err != nil {
					t.Fatal(err)
				}
			}
			res, err := a.RunPromptEx(context.Background(), o, "kimi-coding", 0)
			if err == nil {
				t.Fatal("invalid plan launched", res)
			}
			if which == "budget" && !errors.Is(err, contextplan.ErrBudget) {
				t.Fatal(err)
			}
			if _, err := os.Stat(args); !os.IsNotExist(err) {
				t.Fatal("process launched without admission", err)
			}
		})
	}
}

func TestWorkflowContextExtractKeepsOriginalAndPlan(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ctx := context.Background()
	sid := importTestSkill(t, a, "context-lead")
	bindings := []tasksvc.SkillBinding{{SkillID: sid}}
	w, err := a.Workflows.Save(ctx, "", 0, workflow.Definition{Name: "context", Nodes: []workflow.Node{
		{ID: "a", Kind: "skill", Prompt: strings.Repeat("normal output\n", 1000) + "FAIL TestCritical\nmain.go:42", SkillBindings: bindings},
		{ID: "b", Kind: "skill", Prompt: "Inspect {{steps.a.output}} then {{steps.a.output}}", SkillBindings: bindings, Needs: []workflow.Dependency{{NodeID: "a"}}, Context: &workflow.ContextPolicy{Mode: "summary", MaxTokens: 256}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rid, err := a.Workflows.Start(ctx, w.ID, "", "kimi-coding")
	if err != nil {
		t.Fatal(err)
	}
	var r workflow.Run
	waitFor(t, 20*time.Second, func() bool {
		r, err = a.Workflows.GetRun(ctx, rid)
		return err == nil && r.Status != "queued" && r.Status != "running"
	})
	if r.Status != "succeeded" {
		t.Fatalf("%+v", r)
	}
	var raw string
	if err = a.DB.QueryRow(`SELECT detail_json FROM operation_log WHERE entity_type='run' AND entity_id=? AND action='context.plan'`, r.Nodes[1].ChildRunID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p contextplan.Plan
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	var sources int
	for _, m := range p.Materials {
		if m.Kind == "upstream" {
			sources++
			if m.Ref != "workflow:"+rid+"/node:a" || m.Mode != "summary" {
				t.Fatal(m)
			}
		}
	}
	if sources != 1 || len(r.Nodes[0].Output) < 13000 || len(r.Nodes[1].Output) > 4000 || !strings.Contains(r.Nodes[1].Output, "FAIL TestCritical") || strings.Contains(raw, "FAIL TestCritical") {
		t.Fatal("missing compression/provenance", sources, len(r.Nodes[0].Output), len(r.Nodes[1].Output), raw)
	}
}

func TestContextOptionalSourceAndReferenceArtifacts(t *testing.T) {
	a := newTestApp(t)
	ws := t.TempDir()
	source := "original confidential body"
	n := workflow.Node{ID: "b", ContextRunID: "wf1", Context: &workflow.ContextPolicy{Budget: contextplan.Budget{MaxInputTokens: 200, SystemReserve: 1}}, ContextArtifacts: []workflow.ContextArtifact{{NodeID: "a", Ref: "workflow:wf1/node:a", Body: strings.Repeat(source, 100), Mode: "full", Optional: true}}}
	r, err := workflowContext(nil, n, true, ws)
	if err != nil {
		t.Fatal(err)
	}
	o := runner.Options{Prompt: "inspect [upstream:a]", ContextRequest: r}
	if err = a.planRunContext(context.Background(), &o); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(o.Prompt, source) || !strings.Contains(o.Prompt, "omitted from initial context") {
		t.Fatal(o.Prompt)
	}
	data, err := os.ReadFile(filepath.Join(ws, "context", "upstream-a.txt"))
	if err != nil || string(data) != n.ContextArtifacts[0].Body {
		t.Fatal("original lost", err)
	}
	n.ContextArtifacts[0].Mode = "reference"
	n.ContextArtifacts[0].Optional = false
	r, err = workflowContext(nil, n, true, ws)
	if err != nil {
		t.Fatal(err)
	}
	o = runner.Options{Prompt: "inspect", ContextRequest: r}
	if err = a.planRunContext(context.Background(), &o); err != nil || strings.Contains(o.Prompt, source) || !strings.Contains(o.Prompt, "Reference only") {
		t.Fatal(o.Prompt, err)
	}
}

func TestContextPolicyRPCValidatesAndPersists(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	methods := a.systemRPC()
	if _, err := methods["context.policy.set"](ctx, json.RawMessage(`{"max_input_tokens":-1}`)); err == nil {
		t.Fatal("invalid policy saved")
	}
	if _, err := methods["context.policy.set"](ctx, json.RawMessage(`{"max_input_tokens":5000,"system_reserve":10}`)); err != nil {
		t.Fatal(err)
	}
	got, err := methods["context.policy.get"](ctx, nil)
	if err != nil || got.(contextplan.Budget).MaxInputTokens != 5000 {
		t.Fatal(got, err)
	}
	o := runner.Options{Prompt: strings.Repeat("x", 30000)}
	if err = a.planRunContext(ctx, &o); !errors.Is(err, contextplan.ErrBudget) {
		t.Fatal(err)
	}
}

func TestContextPolicyCorruptionAndArtifactBoundariesFailClosed(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.Auth.SetSetting(ctx, "context.policy", "broken JSON"); err != nil {
		t.Fatal(err)
	}
	o := runner.Options{Prompt: "run"}
	if err := a.planRunContext(ctx, &o); err == nil {
		t.Fatal("corrupt policy ignored")
	}
	for _, source := range []workflow.ContextArtifact{{NodeID: "../escape", Ref: "workflow:w/node:../escape"}, {NodeID: "a", Ref: "workflow:foreign/node:a"}} {
		n := workflow.Node{ID: "b", ContextRunID: "w", Context: &workflow.ContextPolicy{}, ContextArtifacts: []workflow.ContextArtifact{source}}
		if _, err := workflowContext(nil, n, true, t.TempDir()); !errors.Is(err, contextplan.ErrAccess) {
			t.Fatal(err)
		}
	}
	// Prefixing artifact filenames also preserves Windows reserved node IDs.
	n := workflow.Node{ID: "b", ContextRunID: "w", Context: &workflow.ContextPolicy{}, ContextArtifacts: []workflow.ContextArtifact{{NodeID: "CON", Ref: "workflow:w/node:CON", Body: "ok", Mode: "full"}}}
	if _, err := workflowContext(nil, n, true, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestContextCannotChangeAfterPlanning(t *testing.T) {
	a := newTestApp(t)
	o := runner.Options{Prompt: "original", AtStart: func(_ context.Context, o *runner.Options) error { o.Prompt = "changed"; return nil }}
	if err := a.planRunContext(context.Background(), &o); err != nil {
		t.Fatal(err)
	}
	if err := o.AtStart(context.Background(), &o); err == nil || !strings.Contains(err.Error(), "context changed") {
		t.Fatal(err)
	}
}

func TestContextByteLimitRejectsBeforeChangingPrompt(t *testing.T) {
	a := newTestApp(t)
	req := &contextplan.Request{Scope: "execution", Budget: contextplan.Budget{MaxInputTokens: 200000}}
	for _, name := range []string{"a", "b", "c"} {
		req.Materials = append(req.Materials, contextplan.Material{ID: name, Kind: "upstream", Scope: "execution", Allowed: true, Required: true, Body: strings.Repeat(name, 100000)})
	}
	o := runner.Options{Prompt: "original task", ContextRequest: req}
	if err := a.planRunContext(context.Background(), &o); err == nil || !strings.Contains(err.Error(), "256 KiB") {
		t.Fatal(err)
	}
	if o.Prompt != "original task" {
		t.Fatal("rejected plan expanded the prompt")
	}
}
