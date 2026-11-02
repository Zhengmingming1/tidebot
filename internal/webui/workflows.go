package webui

import (
	"encoding/json"
	"errors"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
	"io"
	"net/http"
	"strings"
)

func workflowBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
func workflowError(w http.ResponseWriter, err error) {
	code := 400
	if errors.Is(err, workflow.ErrNotFound) {
		code = 404
	} else if errors.Is(err, workflow.ErrConflict) || errors.Is(err, workflow.ErrNotActive) {
		code = 409
	} else if errors.Is(err, workflow.ErrClosing) {
		code = 503
	}
	writeErr(w, code, err.Error())
}
func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.deps.Workflows == nil {
		writeErr(w, 503, "workflows unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.deps.Workflows.List(r.Context())
		if err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 200, list)
	case http.MethodPost:
		var d workflow.Definition
		if err := workflowBody(w, r, &d); err != nil {
			writeErr(w, 400, "invalid workflow JSON")
			return
		}
		item, err := s.deps.Workflows.Save(r.Context(), "", 0, d)
		if err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 201, item)
	default:
		writeErr(w, 405, "GET or POST only")
	}
}
func (s *Server) handleWorkflowSub(w http.ResponseWriter, r *http.Request) {
	if s.deps.Workflows == nil {
		writeErr(w, 503, "workflows unavailable")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workflows/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	wid := parts[0]
	if len(parts) == 2 && parts[1] == "run" {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "POST only")
			return
		}
		var in struct {
			Input    string `json:"input"`
			Provider string `json:"provider"`
		}
		if err := workflowBody(w, r, &in); err != nil {
			writeErr(w, 400, "invalid run JSON")
			return
		}
		if in.Provider == "" && s.deps.Cfg != nil {
			in.Provider = s.deps.Cfg.DefaultProvider
		}
		if s.deps.Reg != nil {
			if _, ok := s.deps.Reg.Get(in.Provider); !ok {
				writeErr(w, 400, "unknown provider")
				return
			}
		}
		rid, err := s.deps.Workflows.Start(r.Context(), wid, in.Input, in.Provider)
		if err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 202, map[string]string{"run_id": rid})
		return
	}
	if len(parts) != 1 {
		writeErr(w, 404, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.deps.Workflows.Get(r.Context(), wid)
		if err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 200, item)
	case http.MethodPut:
		var in struct {
			ExpectedVersion int                 `json:"expected_version"`
			Definition      workflow.Definition `json:"definition"`
		}
		if err := workflowBody(w, r, &in); err != nil {
			writeErr(w, 400, "invalid workflow JSON")
			return
		}
		item, err := s.deps.Workflows.Save(r.Context(), wid, in.ExpectedVersion, in.Definition)
		if err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 200, item)
	case http.MethodDelete:
		if err := s.deps.Workflows.Delete(r.Context(), wid); err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 405, "GET, PUT or DELETE only")
	}
}
func (s *Server) handleWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	if s.deps.Workflows == nil {
		writeErr(w, 503, "workflows unavailable")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	list, err := s.deps.Workflows.ListRuns(r.Context(), r.URL.Query().Get("workflow_id"))
	if err != nil {
		workflowError(w, err)
		return
	}
	writeJSON(w, 200, list)
}
func (s *Server) handleWorkflowRunSub(w http.ResponseWriter, r *http.Request) {
	if s.deps.Workflows == nil {
		writeErr(w, 503, "workflows unavailable")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workflow-runs/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	if len(parts) == 2 && parts[1] == "stop" {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "POST only")
			return
		}
		if err := s.deps.Workflows.Stop(parts[0]); err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if len(parts) != 1 {
		writeErr(w, 404, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	run, err := s.deps.Workflows.GetRun(r.Context(), parts[0])
	if err != nil {
		workflowError(w, err)
		return
	}
	writeJSON(w, 200, run)
}
