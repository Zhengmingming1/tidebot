package webui

import (
	"net/http"
	"strings"
)

type SkillSetEvalRequest struct {
	SetID     string  `json:"set_id"`
	VersionID string  `json:"version_id"`
	Provider  string  `json:"provider"`
	BudgetUSD float64 `json:"budget_usd"`
}
type EvalAssertion struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Passed bool   `json:"passed"`
}
type EvalRunResult struct {
	RunID           string          `json:"run_id,omitempty"`
	Status          string          `json:"status"`
	Passed          bool            `json:"passed"`
	Assertions      []EvalAssertion `json:"assertions"`
	Output          string          `json:"output"`
	OutputTruncated bool            `json:"output_truncated"`
	DurationMS      int64           `json:"duration_ms"`
	CostUSD         float64         `json:"cost_usd"`
	InputTokens     int             `json:"input_tokens"`
	OutputTokens    int             `json:"output_tokens"`
	Error           string          `json:"error,omitempty"`
}
type EvalCaseResult struct {
	Name        string         `json:"name"`
	Input       string         `json:"input"`
	Baseline    *EvalRunResult `json:"baseline,omitempty"`
	Composition *EvalRunResult `json:"composition,omitempty"`
}
type SkillSetEvaluation struct {
	ID         string                     `json:"id"`
	SetID      string                     `json:"set_id"`
	VersionID  string                     `json:"version_id"`
	SetName    string                     `json:"set_name"`
	Provider   string                     `json:"provider"`
	Model      string                     `json:"model"`
	BudgetUSD  float64                    `json:"budget_usd"`
	CostUSD    float64                    `json:"cost_usd"`
	Status     string                     `json:"status"`
	Error      string                     `json:"error,omitempty"`
	CreatedAt  string                     `json:"created_at"`
	FinishedAt string                     `json:"finished_at,omitempty"`
	Cases      []EvalCaseResult           `json:"cases"`
	Preflight  *DependencyPreflightReport `json:"preflight,omitempty"`
}

func (s *Server) handleSkillSetEvals(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.deps.ListSkillSetEvals == nil {
			writeErr(w, 503, "evaluations unavailable")
			return
		}
		out, err := s.deps.ListSkillSetEvals(r.Context(), r.URL.Query().Get("set_id"))
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodPost:
		if s.deps.StartSkillSetEval == nil {
			writeErr(w, 503, "evaluations unavailable")
			return
		}
		var req SkillSetEvalRequest
		if decodeManifestRequest(w, r, &req) != nil || !idRe.MatchString(req.SetID) || !idRe.MatchString(req.VersionID) {
			writeErr(w, 400, "valid set_id, version_id and evaluation options are required")
			return
		}
		out, err := s.deps.StartSkillSetEval(req)
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 202, out)
	default:
		writeErr(w, 405, "GET or POST only")
	}
}
func (s *Server) handleSkillSetEvalSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/skill-set-evals/"), "/")
	if len(parts) == 0 || !idRe.MatchString(parts[0]) {
		writeErr(w, 404, "evaluation not found")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		if s.deps.GetSkillSetEval == nil {
			writeErr(w, 503, "evaluations unavailable")
			return
		}
		out, err := s.deps.GetSkillSetEval(r.Context(), parts[0])
		if err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if s.deps.CancelSkillSetEval == nil {
			writeErr(w, 503, "evaluations unavailable")
			return
		}
		if err := s.deps.CancelSkillSetEval(parts[0]); err != nil {
			s.setError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	writeErr(w, 405, "GET evaluation or POST cancel only")
}
