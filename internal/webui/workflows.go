package webui

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zhengmingming1/tidebot/internal/guard"
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
	var de *guard.DirError
	if errors.As(err, &de) { // the folder: same answers as the Run dialog
		writeJSON(w, 400, map[string]string{"error": err.Error(), "problem": de.Problem})
		return
	}
	if errors.Is(err, guard.ErrDirBusy) {
		writeJSON(w, 409, map[string]string{"error": err.Error(), "problem": "busy"})
		return
	}
	code := 400
	if errors.Is(err, workflow.ErrNotFound) {
		code = 404
	} else if errors.Is(err, workflow.ErrConflict) || errors.Is(err, workflow.ErrNotActive) {
		code = 409
	} else if errors.Is(err, workflow.ErrClosing) {
		code = 503
	}
	writeErrInfo(w, code, err.Error())
}

// workflowItem is a workflow in the list, with the tasks that run it on a schedule
// or by hand (their schedule and budgets are the workflow's "schedule / budget").
type workflowItem struct {
	workflow.Workflow
	Tasks []workflowTaskRef `json:"tasks"`
}

type workflowTaskRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Cron    string `json:"cron"`
	Status  string `json:"status"`
	Version int    `json:"version"` // pinned workflow version (0 = current)
}

func (s *Server) workflowItems(ctx context.Context, list []workflow.Workflow) []workflowItem {
	byWF := map[string][]workflowTaskRef{}
	if s.deps.Tasks != nil {
		if tasks, err := s.deps.Tasks.List(ctx); err == nil {
			for _, v := range tasks {
				if v.Spec == nil || v.Spec.WorkflowID == "" || v.Task.Status == "archived" {
					continue
				}
				byWF[v.Spec.WorkflowID] = append(byWF[v.Spec.WorkflowID], workflowTaskRef{ID: v.Task.ID, Name: v.Task.Name, Cron: v.Task.CronExpr, Status: v.Task.Status, Version: v.Spec.WorkflowVersion})
			}
		}
	}
	out := make([]workflowItem, 0, len(list))
	for _, w := range list {
		refs := byWF[w.ID]
		if refs == nil {
			refs = []workflowTaskRef{}
		}
		out = append(out, workflowItem{Workflow: w, Tasks: refs})
	}
	return out
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.deps.Workflows == nil {
		writeErrInfo(w, 503, "workflows unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.deps.Workflows.List(r.Context())
		if err != nil {
			workflowError(w, err)
			return
		}
		writeJSON(w, 200, s.workflowItems(r.Context(), list))
	case http.MethodPost:
		var d workflow.Definition
		if err := workflowBody(w, r, &d); err != nil {
			writeErrInfo(w, 400, "invalid workflow JSON")
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
		writeErrInfo(w, 503, "workflows unavailable")
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
			writeErrInfo(w, 400, "invalid run JSON")
			return
		}
		if in.Provider == "" && s.deps.Cfg != nil {
			in.Provider = s.deps.Cfg.DefaultProvider
		}
		if s.deps.Reg != nil {
			if _, ok := s.deps.Reg.Get(in.Provider); !ok {
				writeErrInfo(w, 400, "unknown provider")
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
		writeJSON(w, 200, s.workflowItems(r.Context(), []workflow.Workflow{item})[0])
	case http.MethodPut:
		var in struct {
			ExpectedVersion int                 `json:"expected_version"`
			Definition      workflow.Definition `json:"definition"`
		}
		if err := workflowBody(w, r, &in); err != nil {
			writeErrInfo(w, 400, "invalid workflow JSON")
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
		writeErrInfo(w, 503, "workflows unavailable")
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
	out := make([]workflowRunDTO, len(list))
	for i, run := range list {
		out[i] = workflowRunView(run)
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleWorkflowRunSub(w http.ResponseWriter, r *http.Request) {
	if s.deps.Workflows == nil {
		writeErrInfo(w, 503, "workflows unavailable")
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
	writeJSON(w, 200, workflowRunView(run))
}

// workflowRunDTO adds error codes to an execution, so the page can show stored
// English errors (also on executions recorded before codes existed) in its language.
type workflowRunDTO struct {
	workflow.Run
	ErrorInfo *errInfo          `json:"error_info,omitempty"`
	Nodes     []workflowNodeDTO `json:"nodes"`
}
type workflowNodeDTO struct {
	workflow.NodeRun
	ErrorInfo *errInfo `json:"error_info,omitempty"`
}

func workflowRunView(r workflow.Run) workflowRunDTO {
	out := workflowRunDTO{Run: r, ErrorInfo: explainError(r.Error), Nodes: make([]workflowNodeDTO, len(r.Nodes))}
	for i, n := range r.Nodes {
		out.Nodes[i] = workflowNodeDTO{NodeRun: n, ErrorInfo: explainError(n.Error)}
	}
	return out
}
