package contextplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

func Hash(body string) string { h := sha256.Sum256([]byte(body)); return hex.EncodeToString(h[:]) }

func (Deterministic) Plan(ctx context.Context, req Request) (Plan, error) {
	p := Plan{Policy: PolicyVersion, Estimator: EstimatorVersion, Scope: req.Scope, NodeID: req.NodeID, Provider: req.Provider, Model: req.Model, Materials: []Material{}, Omitted: []Omitted{}, Warnings: []string{"estimated_tokens_not_provider_tokenizer"}}
	var err error
	p.Budget, p.BudgetTokens, err = req.Budget.Resolve()
	if err != nil {
		return p, err
	}
	if req.Scope == "" || len(req.Materials) > 256 {
		return p, errors.New("context scope required and at most 256 materials allowed")
	}
	if p.Budget.WindowTokens == 0 {
		p.Warnings = append(p.Warnings, "model_window_unknown_input_cap_only")
	}
	ms := append([]Material(nil), req.Materials...)
	// Mandatory materials always win over optional duplicates. Ordering never
	// relies on map iteration, wall-clock time, or model relevance guesses.
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if a.Required != b.Required {
			return a.Required
		}
		if !a.Required && a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
	seenIDs := map[string]bool{}
	seen := map[string]string{}
	for _, m := range ms {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if m.ID == "" || seenIDs[m.ID] {
			return p, errors.New("context material IDs must be unique and nonempty")
		}
		seenIDs[m.ID] = true
		if !m.Allowed || m.Scope != req.Scope {
			if m.Required {
				return p, ErrAccess
			}
			p.Omitted = append(p.Omitted, Omitted{ID: m.ID, Reason: "access_denied"})
			continue
		}
		// Avoid loading an already frozen immutable version a second time. Ref
		// is part of identity: equal instruction bytes in different base dirs
		// may refer to different scripts and must never be conflated.
		key := m.Kind + "\x00" + m.Version + "\x00" + m.Hash + "\x00" + m.Ref
		if m.Hash != "" && m.Version != "" && seen[key] != "" {
			p.Omitted = append(p.Omitted, Omitted{ID: m.ID, Reason: "duplicate", DuplicateOf: seen[key]})
			continue
		}
		if !m.Required && p.EstimatedTokens == p.BudgetTokens {
			p.Omitted = append(p.Omitted, Omitted{ID: m.ID, Reason: "budget"})
			continue
		}
		if m.Load != nil {
			m.Body, err = m.Load(ctx)
			if err != nil {
				return p, fmt.Errorf("load context material %s: %w", m.ID, err)
			}
		}
		if len(m.Body) > 1<<20 {
			return p, errors.New("context material exceeds 1 MiB")
		}
		hash := Hash(m.Body)
		if m.Hash != "" && m.Hash != hash {
			return p, errors.New("context snapshot hash mismatch")
		}
		m.Hash = hash
		m.Tokens = Estimate([]byte(m.Body))
		m.Load = nil
		key = m.Kind + "\x00" + m.Version + "\x00" + m.Hash + "\x00" + m.Ref
		if m.Version != "" && seen[key] != "" {
			p.Omitted = append(p.Omitted, Omitted{ID: m.ID, Reason: "duplicate", DuplicateOf: seen[key]})
			continue
		}
		if m.Tokens > p.BudgetTokens-p.EstimatedTokens {
			if m.Required {
				return p, &BudgetError{RequiredTokens: p.EstimatedTokens + m.Tokens, BudgetTokens: p.BudgetTokens}
			}
			p.Omitted = append(p.Omitted, Omitted{ID: m.ID, Reason: "budget"})
			continue
		}
		seen[key] = m.ID
		p.EstimatedTokens += m.Tokens
		p.Materials = append(p.Materials, m)
	}
	// JSON excludes bodies and callbacks: safe to persist even for private inputs.
	b, _ := json.Marshal(p)
	p.ID = Hash(string(b))
	return p, nil
}
