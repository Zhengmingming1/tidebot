package webui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

type DependencyCheckRequest struct {
	Kind          string                 `json:"kind"`
	ID            string                 `json:"id,omitempty"`
	Prompt        string                 `json:"prompt,omitempty"`
	SkillBindings []tasksvc.SkillBinding `json:"skill_bindings,omitempty"`
	Provider      string                 `json:"provider,omitempty"`
	Definition    *workflow.Definition   `json:"definition,omitempty"`
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
	RunID          string              `json:"run_id"`
	ChildRunID     string              `json:"child_run_id"`
	Status         string              `json:"status"`
	Report         *DependencyReport   `json:"report,omitempty"`
	Error          string              `json:"error,omitempty"`
	StaticFindings []DependencyFinding `json:"static_findings,omitempty"`
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
	if r.Definition != nil {
		raw, err := json.Marshal(r.Definition)
		if err != nil || len(raw) > 192<<10 || len(r.Definition.Nodes) > workflow.MaxNodes {
			return errors.New("workflow dependency snapshot is too large")
		}
	}
	return nil
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
