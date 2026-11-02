package webui

import "github.com/Zhengmingming1/tidebot/internal/tasksvc"

// A staged binding proves availability, not that the agent read or invoked it.
// Only immutable run bindings produce loaded evidence; final-answer text never does.
type skillEvidence struct {
	SkillID           string `json:"skill_id"`
	VersionID         string `json:"version_id"`
	Role              string `json:"role"`
	Level             string `json:"level"`
	ExecutionObserved bool   `json:"execution_observed"`
}

func loadedSkillEvidence(bindings []tasksvc.SkillBinding) []skillEvidence {
	out := []skillEvidence{}
	for _, b := range bindings {
		out = append(out, skillEvidence{SkillID: b.SkillID, VersionID: b.VersionID, Role: b.Role, Level: "loaded"})
	}
	return out
}
