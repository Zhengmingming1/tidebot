package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type skillEvalState struct {
	sync.Mutex
	closing bool
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func (a *App) initSkillSetEvals() {
	a.skillEvals.cancels = map[string]context.CancelFunc{}
}

// Recovery is only called after StartDaemon owns IPC; another App.Open may
// belong to a second core that will be refused by the single-instance listener.
func (a *App) recoverSkillSetEvals(ctx context.Context) error {
	_, err := a.DB.ExecContext(ctx, `UPDATE skill_set_evaluations SET status='interrupted',updated_at=? WHERE status='running'`, nowUTC())
	return err
}
func (a *App) stopSkillSetEvals() {
	a.skillEvals.Lock()
	defer a.skillEvals.Unlock()
	a.skillEvals.closing = true
	for _, cancel := range a.skillEvals.cancels {
		cancel()
	}
}
func (a *App) waitSkillSetEvals() { a.skillEvals.wg.Wait() }

func (a *App) getSkillSetEval(ctx context.Context, evalID string) (webui.SkillSetEvaluation, error) {
	var out webui.SkillSetEvaluation
	var raw, status, updated string
	err := a.DB.QueryRowContext(ctx, `SELECT state_json,status,updated_at FROM skill_set_evaluations WHERE id=?`, evalID).Scan(&raw, &status, &updated)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	out.Status = status
	if status == "interrupted" && out.FinishedAt == "" {
		out.FinishedAt = updated
		out.Error = "Tidebot stopped before this evaluation completed; it was not restarted."
		for i := range out.Cases {
			for _, run := range []*webui.EvalRunResult{out.Cases[i].Baseline, out.Cases[i].Composition} {
				if run != nil && run.Status == "running" {
					run.Status = "interrupted"
					run.Passed = false
				}
			}
		}
	}
	return out, nil
}
func (a *App) listSkillSetEvals(ctx context.Context, setID string) ([]webui.SkillSetEvaluation, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id FROM skill_set_evaluations WHERE (?='' OR set_id=?) ORDER BY created_at DESC,id DESC LIMIT 20`, setID, setID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var eid string
		if err = rows.Scan(&eid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, eid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []webui.SkillSetEvaluation{}
	for _, eid := range ids {
		item, err := a.getSkillSetEval(ctx, eid)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
func (a *App) saveSkillSetEval(ev *webui.SkillSetEvaluation) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = a.DB.ExecContext(ctx, `UPDATE skill_set_evaluations SET status=?,state_json=?,updated_at=? WHERE id=?`, ev.Status, string(raw), nowUTC(), ev.ID)
	return err
}
func (a *App) cancelSkillSetEval(evalID string) error {
	a.skillEvals.Lock()
	defer a.skillEvals.Unlock()
	cancel, ok := a.skillEvals.cancels[evalID]
	if !ok {
		return errors.New("evaluation is not running")
	}
	cancel()
	return nil
}

func (a *App) evaluationPreflight(ctx context.Context, set skillstore.SkillSet, providerName string) (webui.DependencyPreflightReport, error) {
	if err := a.Skills.ValidateDependencyBindings(ctx, set.SkillBindings); err != nil {
		return webui.DependencyPreflightReport{}, err
	}
	report, err := a.dependencyPreflight(ctx, webui.DependencyCheckRequest{Kind: "draft", Mode: "read", SkillBindings: set.SkillBindings, Provider: providerName})
	if err != nil {
		return report, err
	}
	// This evaluation has fewer tools than a normal read-only task and no connectors.
	report.RuntimeProfile.AllowedTools = []string{"Read", "Glob", "Grep"}
	report.RuntimeProfile.Notes += " 评测仅允许 Read/Glob/Grep，所有 MCP 关闭。"
	for i := range report.Requirements {
		r := &report.Requirements[i]
		req := r.Requirement
		if req.Kind == "command" || req.Kind == "python_package" || req.Kind == "mcp" || (req.Kind == "tool" && req.Name != "Read" && req.Name != "Glob" && req.Name != "Grep") {
			r.Status = "blocked"
			r.Evidence = "Evaluation permits only Read/Glob/Grep and disables all MCP connectors."
			level := "blocked"
			if req.Optional {
				level = "uncertain"
			}
			report.Findings = append(report.Findings, webui.DependencyFinding{Dependency: req.Name, Kind: "tool", Level: level, Reason: "此依赖在评测环境中被禁用", Evidence: r.Evidence, Suggestion: "评测只验证当前只读环境下的文本输出；需要该依赖时请使用常规任务运行"})
		}
		if !req.Optional && (r.Status == "missing" || r.Status == "blocked") {
			return report, fmt.Errorf("evaluation dependency %s is %s: %s", req.Name, r.Status, r.Evidence)
		}
	}
	for _, f := range report.Findings {
		if f.Level == "missing" || f.Level == "blocked" {
			return report, fmt.Errorf("evaluation preflight: %s: %s", f.Dependency, f.Reason)
		}
	}
	for _, usage := range report.ContextUsage {
		if usage.Exceeded {
			return report, errors.New("evaluation skill context exceeds its limit")
		}
	}
	return report, nil
}

func (a *App) startSkillSetEval(req webui.SkillSetEvalRequest) (webui.SkillSetEvaluation, error) {
	ctx := context.Background()
	if math.IsNaN(req.BudgetUSD) || math.IsInf(req.BudgetUSD, 0) || req.BudgetUSD < 0.02 || req.BudgetUSD > 5 {
		return webui.SkillSetEvaluation{}, errors.New("evaluation budget must be between 0.02 and 5 USD")
	}
	set, err := a.Skills.GetSetVersion(ctx, req.SetID, req.VersionID)
	if err != nil {
		return webui.SkillSetEvaluation{}, err
	}
	if len(set.SkillBindings) < 2 {
		return webui.SkillSetEvaluation{}, errors.New("comparison requires primary and auxiliary skills")
	}
	if len(set.Details.TestCases) == 0 {
		return webui.SkillSetEvaluation{}, errors.New("save at least one test case with non-empty assertions before evaluating")
	}
	if err = skillstore.ValidateSkillSetDetails(set.Details); err != nil {
		return webui.SkillSetEvaluation{}, err
	}
	prof, err := a.ResolveProfile(req.Provider)
	if err != nil {
		return webui.SkillSetEvaluation{}, err
	}
	report, err := a.evaluationPreflight(ctx, set, prof.Name)
	if err != nil {
		return webui.SkillSetEvaluation{}, err
	}
	ev := webui.SkillSetEvaluation{ID: id.New("eval"), SetID: set.ID, VersionID: set.VersionID, SetName: set.Name, Provider: prof.Name, Model: prof.Model, BudgetUSD: req.BudgetUSD, Status: "running", CreatedAt: nowUTC(), Preflight: &report, Cases: []webui.EvalCaseResult{}}
	for _, c := range set.Details.TestCases {
		ev.Cases = append(ev.Cases, webui.EvalCaseResult{Name: c.Name, Input: c.Input})
	}
	a.skillEvals.Lock()
	defer a.skillEvals.Unlock()
	if a.skillEvals.closing {
		return ev, runner.ErrShuttingDown
	}
	if len(a.skillEvals.cancels) >= 1 {
		return ev, errors.New("another skill package evaluation is running; finish or cancel it first")
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	if _, err = a.DB.ExecContext(ctx, `INSERT INTO skill_set_evaluations(id,set_id,version_id,status,state_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, ev.ID, set.ID, set.VersionID, ev.Status, string(raw), ev.CreatedAt, ev.CreatedAt); err != nil {
		return ev, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	a.skillEvals.cancels[ev.ID] = cancel
	a.skillEvals.wg.Add(1)
	// Clone through JSON so callers cannot race the worker's slices and pointers.
	var response webui.SkillSetEvaluation
	_ = json.Unmarshal(raw, &response)
	go a.executeSkillSetEval(runCtx, cancel, ev, set, prof)
	return response, nil
}

func (a *App) executeSkillSetEval(ctx context.Context, cancel context.CancelFunc, ev webui.SkillSetEvaluation, set skillstore.SkillSet, prof provider.Profile) {
	defer a.skillEvals.wg.Done()
	defer func() { cancel(); a.skillEvals.Lock(); delete(a.skillEvals.cancels, ev.ID); a.skillEvals.Unlock() }()
	defer func() {
		ev.FinishedAt = nowUTC()
		if ctx.Err() != nil {
			ev.Status = "canceled"
			a.skillEvals.Lock()
			if a.skillEvals.closing {
				ev.Status = "interrupted"
			}
			a.skillEvals.Unlock()
		}
		if ev.Status == "running" {
			ev.Status = "completed"
		}
		if err := a.saveSkillSetEval(&ev); err != nil {
			a.Log.Error("save skill evaluation", "error", err)
		}
	}()
	perRun := ev.BudgetUSD / float64(len(ev.Cases)*2)
	for i, c := range set.Details.TestCases {
		for _, baseline := range []bool{true, false} {
			if ctx.Err() != nil {
				return
			}
			current, err := a.ResolveProfile(prof.Name)
			if err != nil || !reflect.DeepEqual(current, prof) {
				ev.Status = "failed"
				ev.Error = "Provider configuration changed during evaluation; run a fresh comparison."
				return
			}
			if ev.CostUSD >= ev.BudgetUSD {
				ev.Status = "failed"
				ev.Error = "Evaluation budget exhausted."
				return
			}
			result := &webui.EvalRunResult{Status: "running", Assertions: []webui.EvalAssertion{}}
			if baseline {
				ev.Cases[i].Baseline = result
			} else {
				ev.Cases[i].Composition = result
			}
			if err := a.saveSkillSetEval(&ev); err != nil {
				ev.Status = "failed"
				ev.Error = err.Error()
				return
			}
			cap := math.Min(perRun, ev.BudgetUSD-ev.CostUSD)
			onStart := func(runID string) {
				result.RunID = runID
				if err := a.saveSkillSetEval(&ev); err != nil {
					cancel()
				}
			}
			res, err := a.runEvaluationCase(ctx, set, c.Input, prof.Name, cap, baseline, onStart)
			*result = evaluateTextResult(res, c, a.RunCostUSD(res), err)
			ev.CostUSD += result.CostUSD
			if err := a.saveSkillSetEval(&ev); err != nil {
				ev.Status = "failed"
				ev.Error = err.Error()
				return
			}
			if result.Status != "succeeded" {
				ev.Status = "failed"
				ev.Error = "A comparison run did not succeed; remaining cases were not executed."
				return
			}
		}
	}
}

// Only this comparison path may intentionally omit required auxiliary skills.
// Normal task staging continues to validate the complete dependency closure.
func (a *App) evaluationStage(ctx context.Context, set skillstore.SkillSet, baseline bool) (*skillStage, error) {
	if !baseline {
		return a.stageSkills(ctx, set.SkillBindings)
	}
	if len(set.SkillBindings) < 2 {
		return nil, errors.New("comparison requires primary and auxiliary skills")
	}
	if err := os.MkdirAll(a.skillStageRoot(), 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(a.skillStageRoot(), "evaluation-baseline-")
	if err != nil {
		return nil, err
	}
	skills, err := a.Skills.Stage(ctx, dir, []tasksvc.SkillBinding{set.SkillBindings[0]})
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &skillStage{Dir: dir, Skills: skills}, nil
}
func (a *App) runEvaluationCase(ctx context.Context, set skillstore.SkillSet, input, providerName string, budget float64, baseline bool, onStart func(string)) (runner.Result, error) {
	st, err := a.evaluationStage(ctx, set, baseline)
	if err != nil {
		return runner.Result{}, err
	}
	defer st.Close()
	args, err := st.CheckedArgs(a.modernClaude(), false)
	if err != nil {
		return runner.Result{}, err
	}
	ws := filepath.Join(st.Dir, "work")
	if err = os.MkdirAll(ws, 0o700); err != nil {
		return runner.Result{}, err
	}
	primary := st.Skills[0]
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var recordErr error
	res, err := a.RunPromptEx(runCtx, runner.Options{Prompt: input, DisplayPrompt: input, PromptStdin: true, WorkspaceDir: ws, TriggerKind: "skill_set_eval", CostBucket: cost.BucketAssist, NoPluginMCP: true, RunMode: "read", SkillID: primary.SkillID, SkillVersionID: primary.VersionID, ContextArgs: args,
		ExtraArgs: []string{"--permission-mode", "dontAsk", "--permission-prompts", "none", "--restricted", "--tools", "Read,Glob,Grep", "--allowedTools", "Read,Glob,Grep", "--disallowedTools", strings.Join(readOnlyDeny, ","), "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--max-budget-usd", strconv.FormatFloat(budget, 'f', 6, 64), "--effort", "low"},
		OnStart: func(runID string) {
			if err := a.Skills.RecordRunBindings(context.Background(), runID, st.Bindings()); err != nil {
				recordErr = err
				cancel()
			}
			onStart(runID)
		},
	}, providerName, 3*time.Minute)
	if recordErr != nil {
		return res, recordErr
	}
	return res, err
}
func evaluateTextResult(res runner.Result, c skillstore.EvalCase, costUSD float64, err error) webui.EvalRunResult {
	out := webui.EvalRunResult{RunID: res.RunID, Status: res.Status, Output: res.ResultText, DurationMS: res.DurationMS, CostUSD: costUSD, InputTokens: res.InputTokens, OutputTokens: res.OutputTokens, Assertions: []webui.EvalAssertion{}, Passed: err == nil && res.Status == "succeeded"}
	if err != nil {
		out.Error = err.Error()
	}
	if out.Status == "" {
		out.Status = "failed"
	}
	for _, a := range c.MustContain {
		ok := strings.TrimSpace(a) != "" && strings.Contains(res.ResultText, a)
		out.Assertions = append(out.Assertions, webui.EvalAssertion{Kind: "must_contain", Text: a, Passed: ok})
		out.Passed = out.Passed && ok
	}
	for _, a := range c.MustNotContain {
		ok := strings.TrimSpace(a) != "" && !strings.Contains(res.ResultText, a)
		out.Assertions = append(out.Assertions, webui.EvalAssertion{Kind: "must_not_contain", Text: a, Passed: ok})
		out.Passed = out.Passed && ok
	}
	if len(out.Assertions) == 0 {
		out.Passed = false
	}
	if len(out.Output) > 64<<10 {
		out.Output = dependencyClip(out.Output, 64<<10)
		out.OutputTruncated = true
	}
	return out
}
