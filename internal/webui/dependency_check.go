package webui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

type DependencyCheckRequest struct {
	Kind              string                 `json:"kind"`
	ID                string                 `json:"id,omitempty"`
	Prompt            string                 `json:"prompt,omitempty"`
	SkillBindings     []tasksvc.SkillBinding `json:"skill_bindings,omitempty"`
	Provider          string                 `json:"provider,omitempty"`
	Definition        *workflow.Definition   `json:"definition,omitempty"`
	Workspace         string                 `json:"workspace,omitempty"`
	Mode              string                 `json:"mode,omitempty"`
	PythonEnvironment string                 `json:"python_environment,omitempty"`
	Execution         string                 `json:"execution,omitempty"`     // draft: quick (default) | task
	AllowedTools      []string               `json:"allowed_tools,omitempty"` // draft task policy preview only
}

// RuntimeProfile describes the target run, never the tool-free AI checker.
// AllowedTools are pre-approved rules, not an exhaustive list of available tools.
type DependencyRuntimeProfile struct {
	Provider          string   `json:"provider,omitempty"`
	Mode              string   `json:"mode"`
	Workspace         string   `json:"workspace,omitempty"`
	WorkspaceKind     string   `json:"workspace_kind"`
	PythonEnvironment string   `json:"python_environment,omitempty"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	DeniedTools       []string `json:"denied_tools,omitempty"`
	Restricted        bool     `json:"restricted"`
	Notes             string   `json:"notes"`
}

type DependencyContextMember struct {
	SkillID         string `json:"skill_id"`
	VersionID       string `json:"version_id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	Bytes           int    `json:"bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

type DependencyContextUsage struct {
	Origin          string                    `json:"origin"`
	Members         []DependencyContextMember `json:"members"`
	TotalBytes      int                       `json:"total_bytes"`
	EstimatedTokens int                       `json:"estimated_tokens"`
	LimitBytes      int                       `json:"limit_bytes"`
	Exceeded        bool                      `json:"exceeded"`
	EstimateOnly    bool                      `json:"estimate_only"`
	EstimateMethod  string                    `json:"estimate_method"`
}

type DependencyRequirementResult struct {
	SkillID     string                        `json:"skill_id"`
	VersionID   string                        `json:"version_id"`
	Requirement skillstore.RuntimeRequirement `json:"requirement"`
	Status      string                        `json:"status"` // present | missing | blocked | unchecked
	Evidence    string                        `json:"evidence"`
}

type DependencyPreflightReport struct {
	Truncated      bool                          `json:"truncated"`
	RuntimeProfile DependencyRuntimeProfile      `json:"runtime_profile"`
	ContextUsage   []DependencyContextUsage      `json:"context_usage"`
	Requirements   []DependencyRequirementResult `json:"requirements"`
	Findings       []DependencyFinding           `json:"findings"`
}

type DependencyFinding struct {
	Dependency string `json:"dependency"`
	Kind       string `json:"kind"`
	Level      string `json:"level"`
	Reason     string `json:"reason"`
	Evidence   string `json:"evidence"`
	Suggestion string `json:"suggestion"`
}

type DependencyReport struct {
	Summary  string              `json:"summary"`
	Findings []DependencyFinding `json:"findings"`
}

type DependencyCheckStatus struct {
	RunID          string                     `json:"run_id"`
	ChildRunID     string                     `json:"child_run_id"`
	Status         string                     `json:"status"`
	Report         *DependencyReport          `json:"report,omitempty"`
	Error          string                     `json:"error,omitempty"`
	StaticFindings []DependencyFinding        `json:"static_findings,omitempty"`
	Preflight      *DependencyPreflightReport `json:"preflight,omitempty"`
}

// Validate bounds the supplied snapshot, but deliberately leaves invalid bindings
// and graphs to the checker: those are problems the user asked it to explain.
func (r DependencyCheckRequest) Validate() error {
	switch r.Kind {
	case "task", "skill", "skill_set", "workflow", "button":
		if !idRe.MatchString(r.ID) {
			return errors.New("a valid target id is required")
		}
	case "draft":
		if r.ID != "" && !idRe.MatchString(r.ID) {
			return errors.New("invalid task draft id")
		}
		if strings.TrimSpace(r.Prompt) == "" && len(r.SkillBindings) == 0 {
			return errors.New("provide a prompt or skills to analyze")
		}
	case "workflow_draft":
		if r.Definition == nil {
			return errors.New("workflow definition is required")
		}
	default:
		return errors.New("unsupported dependency check kind")
	}
	if len(r.Prompt) > 64<<10 || len(r.Provider) > 80 || len(r.SkillBindings) > 32 {
		return errors.New("dependency check input is too large")
	}
	if len(r.Workspace) > 4096 || len(r.PythonEnvironment) > 4096 || strings.ContainsAny(r.Workspace+r.PythonEnvironment, "\x00\r\n") {
		return errors.New("invalid runtime path")
	}
	if r.Mode != "" && r.Mode != "read" && r.Mode != "edit" && r.Mode != "exec" {
		return errors.New("runtime mode must be read, edit or exec")
	}
	if r.Execution != "" && r.Execution != "quick" && r.Execution != "task" {
		return errors.New("execution must be quick or task")
	}
	if len(r.AllowedTools) > 64 {
		return errors.New("too many allowed tool rules")
	}
	for _, rule := range r.AllowedTools {
		if len(rule) > 256 || strings.ContainsAny(rule, "\x00\r\n") {
			return errors.New("invalid allowed tool rule")
		}
	}
	if r.PythonEnvironment != "" && !filepath.IsAbs(r.PythonEnvironment) {
		return errors.New("python_environment must be an absolute virtual environment directory")
	}
	if r.Definition != nil {
		raw, err := json.Marshal(r.Definition)
		if err != nil || len(raw) > 192<<10 || len(r.Definition.Nodes) > workflow.MaxNodes {
			return errors.New("workflow dependency snapshot is too large")
		}
	}
	return nil
}

// handleDependencyPreflight performs only deterministic local reads. It never
// launches the model, imports a package, executes a command or installs anything.
func (s *Server) handleDependencyPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST only")
		return
	}
	if s.deps.DependencyPreflight == nil {
		writeErr(w, http.StatusServiceUnavailable, "dependency preflight is unavailable")
		return
	}
	var req DependencyCheckRequest
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		req = DependencyCheckRequest{Kind: q.Get("kind"), ID: q.Get("id"), Workspace: q.Get("workspace"), Mode: q.Get("mode"), PythonEnvironment: q.Get("python_environment")}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		defer r.Body.Close()
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid dependency check input")
			return
		}
		if err := d.Decode(new(any)); err != io.EOF {
			writeErr(w, http.StatusBadRequest, "one JSON object is required")
			return
		}
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.deps.DependencyPreflight(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDependencyCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.deps.StartDependencyCheck == nil {
		writeErr(w, http.StatusServiceUnavailable, "dependency checking is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	defer r.Body.Close()
	var req DependencyCheckRequest
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid dependency check input")
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "one JSON object is required")
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	runID, err := s.deps.StartDependencyCheck(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if runID == "" {
		writeErr(w, http.StatusServiceUnavailable, "dependency check did not start")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
}

func (s *Server) handleDependencyCheckStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.deps.DependencyCheckStatus == nil {
		writeErr(w, http.StatusServiceUnavailable, "dependency checking is unavailable")
		return
	}
	runID := strings.TrimPrefix(r.URL.Path, "/api/dependencies/check/")
	if !idRe.MatchString(runID) {
		writeErr(w, http.StatusBadRequest, "invalid dependency check id")
		return
	}
	out, err := s.deps.DependencyCheckStatus(r.Context(), runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "dependency check not found")
		} else {
			writeErr(w, http.StatusInternalServerError, "cannot read dependency check")
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}
