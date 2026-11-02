package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

func (st *skillStage) contextRequest(modern, explicit bool) *contextplan.Request {
	r := &contextplan.Request{Scope: "execution"}
	if st == nil {
		return r
	}
	wrapperTokens := contextplan.Estimate([]byte(st.contextText(modern, explicit)))
	for i, s := range st.Skills {
		body := strings.TrimSpace(string(s.SkillMD))
		r.Materials = append(r.Materials, contextplan.Material{ID: "skill:" + s.SkillID, Kind: "skill", Scope: r.Scope, Allowed: true, Required: true, Order: i + 1, Version: s.VersionID, Ref: "skill:" + s.SkillID + "@" + s.VersionID, Body: body})
		if !(modern && explicit && i == 0) {
			wrapperTokens -= contextplan.Estimate([]byte(body))
		}
	}
	// Fixed wrapper allowance covers the per-run stage path and slash command.
	r.Materials = append(r.Materials, contextplan.Material{ID: "skill-wrappers", Kind: "reservation", Scope: r.Scope, Allowed: true, Required: true, Body: strings.Repeat(" ", 4*(max(0, wrapperTokens)+64))})
	return r
}

func workflowContext(st *skillStage, n workflow.Node, modern bool, ws string) (*contextplan.Request, error) {
	r := st.contextRequest(modern, true)
	r.NodeID = n.ID
	if n.Context == nil {
		return r, nil
	}
	if n.ContextRunID == "" {
		return nil, contextplan.ErrAccess
	}
	r.Scope = "workflow:" + n.ContextRunID + "/node:" + n.ID
	r.Budget = n.Context.Budget
	for i := range r.Materials {
		r.Materials[i].Scope = r.Scope
	}
	for i, source := range n.ContextArtifacts {
		if !contextArtifactID.MatchString(source.NodeID) || source.Ref != "workflow:"+n.ContextRunID+"/node:"+source.NodeID {
			return nil, contextplan.ErrAccess
		}
		// IDs were validated by the workflow service. OpenRoot is also a hard
		// filesystem boundary if an internal caller bypasses that validation.
		root, err := os.OpenRoot(ws)
		if err != nil {
			return nil, err
		}
		err = root.MkdirAll("context", 0700)
		rel := filepath.Join("context", "upstream-"+source.NodeID+".txt")
		if err == nil {
			err = root.WriteFile(rel, []byte(source.Body), 0600)
		}
		root.Close()
		if err != nil {
			return nil, err
		}
		ref := source.Ref
		body := source.Body
		switch source.Mode {
		case "summary":
			body = contextplan.Compact(body, ref, source.MaxTokens)
		case "reference":
			body = "[Reference only; original content is not loaded.]"
		}
		body = fmt.Sprintf("\n[upstream:%s]\nSource: %s\nOriginal SHA-256: %s\nOriginal file: %s\n%s\n", source.NodeID, ref, contextplan.Hash(source.Body), filepath.ToSlash(rel), body)
		r.Materials = append(r.Materials, contextplan.Material{ID: "upstream:" + source.NodeID, Kind: "upstream", Mode: source.Mode, Scope: r.Scope, Allowed: true, Required: !source.Optional, Priority: source.Priority, Order: 100 + i, Version: contextplan.Hash(source.Body), Ref: ref, Body: body})
	}
	return r, nil
}

var contextArtifactID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,47}$`)

// planRunContext is the one admission boundary for initial model context.
// No workspace scan, memory search, credential read or model call occurs here.
func (a *App) planRunContext(ctx context.Context, opts *runner.Options) error {
	req := contextplan.Request{Scope: "execution"}
	if opts.ContextRequest != nil {
		req = *opts.ContextRequest
		req.Materials = append([]contextplan.Material(nil), req.Materials...)
	}
	base, err := a.contextBudget(ctx)
	if err != nil {
		return err
	}
	req.Budget = base.Overlay(req.Budget)
	req.Provider, req.Model = opts.Profile.Name, opts.Profile.Model
	// Task and safety instructions are indivisible and never optional.
	req.Materials = append(req.Materials, contextplan.Material{ID: "task", Kind: "task", Scope: req.Scope, Allowed: true, Required: true, Order: -1, Body: opts.Prompt})
	// Reserve omission notices before selection so the actual emitted context
	// stays within the planned budget even when every optional source is absent.
	var notices strings.Builder
	for _, m := range req.Materials {
		if !m.Required {
			fmt.Fprintf(&notices, "\n[%s: omitted from initial context; inspect its original artifact if needed.]", m.ID)
		}
	}
	if notices.Len() > 0 {
		req.Materials = append(req.Materials, contextplan.Material{ID: "selection-notices", Kind: "reservation", Scope: req.Scope, Allowed: true, Required: true, Body: notices.String()})
	}
	plan, err := (contextplan.Deterministic{}).Plan(ctx, req)
	if err != nil {
		return err
	}
	for _, m := range plan.Materials {
		if m.Kind == "upstream" {
			opts.Prompt += m.Body
		}
	}
	if notices.Len() > 0 {
		var noticeOutput strings.Builder
		for _, omitted := range plan.Omitted {
			fmt.Fprintf(&noticeOutput, "\n[%s: omitted from initial context; inspect its original artifact if needed.]", omitted.ID)
		}
		opts.Prompt += noticeOutput.String()
	}
	if len(opts.Prompt) > workflow.MaxPromptBytes {
		return fmt.Errorf("planned prompt exceeds %d KiB", workflow.MaxPromptBytes>>10)
	}
	if req.NodeID != "" {
		opts.PromptStdin = true
	}
	audit, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	before := opts.AtStart
	plannedPrompt := opts.Prompt
	plannedArgs := strings.Join(opts.ContextArgs, "\x00")
	opts.AtStart = func(c context.Context, o *runner.Options) error {
		if before != nil {
			if err := before(c, o); err != nil {
				return err
			}
		}
		if o.Prompt != plannedPrompt || strings.Join(o.ContextArgs, "\x00") != plannedArgs {
			return fmt.Errorf("context changed after planning; execution refused")
		}
		_, err := a.DB.ExecContext(c, `INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES(?,'run',?,'context.plan','context-planner',?)`, time.Now().UTC().Format(time.RFC3339), o.RunID, string(audit))
		if err != nil {
			return fmt.Errorf("cannot persist context plan: %w", err)
		}
		return nil
	}
	return nil
}

func (a *App) contextBudget(ctx context.Context) (contextplan.Budget, error) {
	var b contextplan.Budget
	raw, err := a.Auth.GetSetting(ctx, "context.policy")
	if err != nil {
		return b, fmt.Errorf("cannot read context policy: %w", err)
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &b); err != nil {
			return b, fmt.Errorf("invalid stored context policy: %w", err)
		}
	}
	return b, nil
}
