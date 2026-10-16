package tasksvc

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
	SkillID   string `json:"skill_id"`
	SkillName string `json:"skill_name,omitempty"`
	Mode      string `json:"mode"` // follow|pinned
	VersionID string `json:"version_id,omitempty"`
}
