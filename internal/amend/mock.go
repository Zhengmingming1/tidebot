package amend

import (
	"context"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// MockStructurer produces deterministic items for tests (no network).
type MockStructurer struct{}

func (MockStructurer) Structure(_ context.Context, _ string, spec *tasksvc.Spec, utterance string) (StructuredProposal, error) {
	u := strings.TrimSpace(utterance)
	lower := strings.ToLower(u)
	scope := "permanent"
	if strings.Contains(lower, "this run") || strings.Contains(lower, "仅本次") || strings.Contains(lower, "only this") || strings.Contains(lower, "next time") {
		scope = "next_run"
	}
	op := "add"
	var target string
	var conflicts []string
	if strings.Contains(lower, "instead") || strings.Contains(lower, "replace") || strings.Contains(lower, "改成") {
		op = "modify"
		if spec != nil && len(spec.Constraints) > 0 {
			target = spec.Constraints[0].ID
			conflicts = []string{target}
		}
	}
	if strings.Contains(lower, "remove") || strings.Contains(lower, "delete") || strings.Contains(lower, "删除") {
		op = "delete"
		if spec != nil && len(spec.Constraints) > 0 {
			target = spec.Constraints[0].ID
		}
	}
	return StructuredProposal{Items: []ProposedItem{{
		Op: op, Scope: scope, TargetSpecItem: target, Content: u, ConflictWith: conflicts,
	}}}, nil
}
