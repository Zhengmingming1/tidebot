package tasksvc

import (
	"fmt"
	"regexp"
	"strings"
)

// Spec is the immutable task content stored in versions.content_json.
type Spec struct {
	Goal           string           `json:"goal"`
	Prompt         string           `json:"prompt"`
	Constraints    []SpecConstraint `json:"constraints,omitempty"`
	SkillBindings  []SkillBinding   `json:"skill_bindings,omitempty"`
	Provider       string           `json:"provider,omitempty"`
	WorkspaceDir   string           `json:"workspace_dir,omitempty"`
	TimeoutMinutes int              `json:"timeout_minutes,omitempty"`
	AllowedTools   []string         `json:"allowed_tools,omitempty"`
	MaxBudgetUSD   float64          `json:"max_budget_usd,omitempty"`
}

// SpecConstraint is a named requirement inside the spec.
type SpecConstraint struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// SkillBinding is follow current or pinned to a skill version.
type SkillBinding struct {
	Role string `json:"role,omitempty"` // primary|auxiliary; legacy empty roles use the first binding as primary

	SkillID   string `json:"skill_id"`
	SkillName string `json:"skill_name,omitempty"`
	Mode      string `json:"mode"` // follow|pinned
	VersionID string `json:"version_id,omitempty"`
}

// NormalizeBindings validates the explicit composition and preserves legacy single-skill specs.
func NormalizeBindings(in []SkillBinding) ([]SkillBinding, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > 16 {
		return nil, fmt.Errorf("at most 16 skills may be attached")
	}
	out := append([]SkillBinding(nil), in...)
	explicit := false
	for _, b := range out {
		if b.Role != "" {
			explicit = true
		}
	}
	seen := map[string]bool{}
	primary := -1
	for i := range out {
		b := &out[i]
		b.SkillID = strings.TrimSpace(b.SkillID)
		if !bindingID.MatchString(b.SkillID) || seen[b.SkillID] {
			return nil, fmt.Errorf("invalid or duplicate skill id: %s", b.SkillID)
		}
		seen[b.SkillID] = true
		if b.Mode == "" {
			b.Mode = "follow"
		}
		if b.Mode != "follow" && b.Mode != "pinned" {
			return nil, fmt.Errorf("skill mode must be follow or pinned")
		}
		if b.Mode == "pinned" && !bindingID.MatchString(b.VersionID) {
			return nil, fmt.Errorf("pinned skill requires a version")
		}
		if b.Mode == "follow" {
			b.VersionID = ""
		}
		if b.Role == "" {
			b.Role = "auxiliary"
			if !explicit && i == 0 {
				b.Role = "primary"
			}
		}
		switch b.Role {
		case "primary":
			if primary >= 0 {
				return nil, fmt.Errorf("choose exactly one primary skill")
			}
			primary = i
		case "auxiliary":
		default:
			return nil, fmt.Errorf("skill role must be primary or auxiliary")
		}
	}
	if primary < 0 {
		return nil, fmt.Errorf("choose exactly one primary skill")
	}
	if primary > 0 {
		main := out[primary]
		copy(out[1:primary+1], out[:primary])
		out[0] = main
	}
	return out, nil
}

var bindingID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
