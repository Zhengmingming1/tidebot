package workflow

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
)

type ContextPolicy struct {
	Budget    contextplan.Budget `json:"budget,omitempty"`
	Mode      string             `json:"mode,omitempty"`       // full (default), summary, reference
	MaxTokens int                `json:"max_tokens,omitempty"` // per summary; default 2048
	Sources   []ContextSource    `json:"sources,omitempty"`
}
type ContextSource struct {
	NodeID    string `json:"node_id"`
	Mode      string `json:"mode,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Priority  int    `json:"priority,omitempty"`
}
type ContextArtifact struct {
	NodeID, Ref, Body, Mode string
	MaxTokens, Priority     int
	Optional                bool
}

func validateContext(n Node, ancestors map[string]bool) error {
	p := n.Context
	if p == nil {
		return nil
	}
	if n.Kind != "skill" {
		return fmt.Errorf("context policy requires a skill node")
	}
	if _, _, err := p.Budget.Resolve(); err != nil {
		return err
	}
	valid := func(mode string, tokens int) bool {
		return (mode == "" || mode == "full" || mode == "summary" || mode == "reference") && (tokens == 0 || tokens >= 256 && tokens <= 65536)
	}
	if !valid(p.Mode, p.MaxTokens) || len(p.Sources) > MaxNodes {
		return fmt.Errorf("invalid context mode or summary token limit (256-65536)")
	}
	seen := map[string]bool{}
	for _, s := range p.Sources {
		if !ancestors[s.NodeID] || seen[s.NodeID] || !valid(s.Mode, s.MaxTokens) || s.Priority < 0 || s.Priority > 100 {
			return fmt.Errorf("invalid or unauthorized context source %s", s.NodeID)
		}
		seen[s.NodeID] = true
	}
	return nil
}

// prepareContext runs on the graph owner goroutine. It copies only explicitly
// referenced/selected ancestor outputs, never sharing the live states map with
// executors. Repeated placeholders become one material with stable markers.
func prepareContext(n Node, rid, input string, states map[string]*NodeRun) (Node, string, error) {
	if n.Context == nil {
		p, e := render(n.Prompt, input, states, n.Join)
		return n, p, e
	}
	p := n.Context
	selected := map[string]ContextSource{}
	for _, m := range placeholder.FindAllStringSubmatch(n.Prompt, -1) {
		if id, ok := stepRef(strings.TrimSpace(m[1])); ok {
			selected[id] = ContextSource{NodeID: id}
		}
	}
	for _, s := range p.Sources {
		selected[s.NodeID] = s
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	n.ContextRunID = rid
	for _, id := range ids {
		s := selected[id]
		state := states[id]
		if state == nil {
			return n, "", contextplan.ErrAccess
		}
		if state.Status == "skipped" && n.Join == "any" {
			continue
		}
		if state.Status != "succeeded" {
			return n, "", fmt.Errorf("context output %s is unavailable", id)
		}
		mode := s.Mode
		if mode == "" {
			mode = p.Mode
		}
		if mode == "" {
			mode = "full"
		}
		limit := s.MaxTokens
		if limit == 0 {
			limit = p.MaxTokens
		}
		if limit == 0 {
			limit = 2048
		}
		n.ContextArtifacts = append(n.ContextArtifacts, ContextArtifact{NodeID: id, Ref: "workflow:" + rid + "/node:" + id, Body: state.Output, Mode: mode, MaxTokens: limit, Priority: s.Priority, Optional: s.Optional})
	}
	// Replace only trusted template syntax; do not reinterpret upstream content.
	template := placeholder.ReplaceAllStringFunc(n.Prompt, func(match string) string {
		if id, ok := stepRef(strings.TrimSpace(match[2 : len(match)-2])); ok {
			if state := states[id]; state != nil && state.Status == "skipped" {
				return "[upstream:" + id + " omitted: branch not selected]"
			}
			return "[upstream:" + id + "]"
		}
		return match
	})
	prompt, err := render(template, input, nil, n.Join)
	return n, prompt, err
}
