package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/optimize"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

type skillChoice struct {
	SkillID, VersionID string
	Candidate          bool
}

// chooseSkillVersions resolves follow-bindings to concrete versions (canary routing).
func (a *App) chooseSkillVersions(ctx context.Context, taskID, bucket string, in []tasksvc.SkillBinding) ([]tasksvc.SkillBinding, []skillChoice) {
	noCanary := bucket == cost.BucketDryRun || (a.Policy != nil && a.Policy.Get(ctx, taskID).SideEffects)
	out := make([]tasksvc.SkillBinding, 0, len(in))
	var ch []skillChoice
	for _, b := range in {
		if b.Mode == "pinned" && b.VersionID != "" {
			out = append(out, b)
			continue
		}
		vid, cand, err := a.Optimize.Choose(ctx, b.SkillID, noCanary)
		if err != nil {
			out = append(out, b)
			continue
		}
		b2 := b
		b2.Mode, b2.VersionID = "pinned", vid
		out = append(out, b2)
		ch = append(ch, skillChoice{SkillID: b.SkillID, VersionID: vid, Candidate: cand})
	}
	return out, ch
}

// recordSkillOutcomes feeds canary metrics; next-run-only runs never enter the baseline.
func (a *App) recordSkillOutcomes(ctx context.Context, ch []skillChoice, res runner.Result, err error, nextRun bool) {
	if res.RunID == "" || errors.Is(err, runner.ErrStoppedByUser) || errors.Is(err, runner.ErrShuttingDown) || errors.Is(err, runner.ErrSkipped) {
		return // stopped from outside: says nothing about the skill
	}
	class := ""
	if err != nil {
		class, _ = policy.Classify(err.Error())
	}
	for _, c := range ch {
		if nextRun && !c.Candidate {
			continue
		}
		_ = a.Optimize.Record(ctx, optimize.Outcome{RunID: res.RunID + ":" + c.SkillID, SkillID: c.SkillID, VersionID: c.VersionID,
			Candidate: c.Candidate, OK: err == nil, Blocked: class == "budget" || class == "danger_blocked",
			CostUSD: a.RunCostUSD(res), DurationMS: res.DurationMS})
	}
}

func (a *App) skillName(id string) string {
	var n string
	_ = a.DB.QueryRow(`SELECT name FROM skills WHERE id=?`, id).Scan(&n)
	return n
}

// AnalyzeSkill runs the analyzer (read-only, JSON schema, optimization bucket) and files a proposal.
func (a *App) AnalyzeSkill(ctx context.Context, skillID, instruction string) (*optimize.Proposal, error) {
	cur, err := a.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	if err != nil {
		return nil, errors.New("skill not found")
	}
	files, err := a.Skills.Files(ctx, cur.ID)
	if err != nil {
		return nil, err
	}
	var n, ok int
	var avgCost, avgDur float64
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(ok),0), COALESCE(AVG(cost_usd),0), COALESCE(AVG(duration_ms),0)
FROM canary_runs WHERE skill_id=? AND version_id=? AND is_candidate=0`, skillID, cur.ID).Scan(&n, &ok, &avgCost, &avgDur)
	metrics := fmt.Sprintf("runs=%d succeeded=%d avg_cost_usd=%.4f avg_duration_ms=%.0f", n, ok, avgCost, avgDur)
	prompt := optimize.BuildAnalyzerPrompt(a.skillName(skillID), files, metrics, instruction)
	res, err := a.RunPromptEx(ctx, runner.Options{
		Prompt: prompt, TriggerKind: "optimization", CostBucket: cost.BucketOptimization,
		ExtraArgs: []string{"--permission-mode", "dontAsk", "--permission-prompts", "none",
			"--allowedTools", strings.Join(optimize.AnalyzerTools, ","), "--json-schema", optimize.AnalyzerSchema,
			"--max-budget-usd", "0.10", "--effort", "medium"},
	}, "", 10*time.Minute)
	if err != nil {
		return nil, err
	}
	an, err := optimize.ParseAnalysis(res.ResultText)
	if err != nil {
		return nil, err
	}
	src := "auto"
	if instruction != "" {
		src = "user"
	}
	a.Optimize.MarkAnalyzed(ctx, skillID)
	return a.Optimize.Propose(ctx, skillID, an.Apply(files), src, an.Category, an.Rationale)
}

// runOptimizer checks the long-run trigger hourly.
func (a *App) runOptimizer(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			rows, err := a.DB.QueryContext(ctx, `SELECT id FROM skills WHERE optimize_mode != 'off'`)
			if err != nil {
				continue
			}
			var ids []string
			for rows.Next() {
				var x string
				_ = rows.Scan(&x)
				ids = append(ids, x)
			}
			rows.Close()
			for _, sid := range ids {
				if ok, why := a.Optimize.ShouldAnalyze(ctx, sid, now); ok {
					if _, err := a.AnalyzeSkill(ctx, sid, ""); err != nil {
						a.Log.Warn("skill analysis", "skill", sid, "reason", why, "error", err)
					}
				}
			}
		}
	}
}

type skillReq struct {
	SkillID     string  `json:"skill_id"`
	ProposalID  string  `json:"proposal_id"`
	Mode        string  `json:"mode"`
	Ratio       float64 `json:"canary_ratio"`
	Runs        int     `json:"canary_runs"`
	Instruction string  `json:"instruction"`
	N           int     `json:"n"`
}

func (a *App) skillRPC(f func(context.Context, skillReq) (any, error)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		r, err := decode[skillReq](raw)
		if err != nil {
			return nil, err
		}
		return f(ctx, r)
	}
}

func (a *App) registerSkillRPC(reg func(string, func(context.Context, json.RawMessage) (any, error))) {
	ok := map[string]bool{"ok": true}
	reg("skills.optimize_settings", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) {
		if r.Mode != "" {
			if err := a.Optimize.SetSettings(ctx, r.SkillID, optimize.Settings{Mode: r.Mode, Ratio: r.Ratio, Runs: r.Runs}); err != nil {
				return nil, err
			}
		}
		return a.Optimize.GetSettings(ctx, r.SkillID)
	}))
	reg("skills.propose", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) {
		return a.AnalyzeSkill(ctx, r.SkillID, r.Instruction)
	}))
	reg("skills.proposals", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) { return a.Optimize.Proposals(ctx, r.SkillID) }))
	reg("skills.canary_start", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) {
		return ok, a.Optimize.StartCanary(ctx, r.ProposalID)
	}))
	reg("skills.promote", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) { return ok, a.Optimize.Promote(ctx, r.ProposalID) }))
	reg("skills.discard", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) { return ok, a.Optimize.Discard(ctx, r.ProposalID) }))
	reg("skills.extend", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) { return ok, a.Optimize.Extend(ctx, r.SkillID, r.N) }))
	reg("skills.compare", a.skillRPC(func(ctx context.Context, r skillReq) (any, error) { return a.Optimize.Compare(ctx, r.SkillID) }))
}
