package webui

import (
	"encoding/json"

	"io"

	"net/http"

	"strings"

	"github.com/Zhengmingming1/tidebot/internal/auth"

	"github.com/Zhengmingming1/tidebot/internal/cost"

	"github.com/Zhengmingming1/tidebot/internal/version"
)

// ---------- tasks ----------

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.createTask(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method")
		return
	}
	list, err := s.deps.Tasks.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []taskDTO{}
	for _, v := range list {
		if v.Task.Status == "archived" {
			continue
		}
		out = append(out, toTaskDTO(v))
	}
	if err := s.enrichTaskList(r.Context(), out); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

type versionDTO struct {
	ID        string `json:"id"`
	No        int    `json:"no"`
	ParentID  string `json:"parent_id"`
	Source    string `json:"source"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleTaskSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	ctx := r.Context()
	if id == "export" && len(parts) == 1 && r.Method == http.MethodGet {
		s.exportTasks(w, r)
		return
	}
	if id == "import" && len(parts) == 1 && r.Method == http.MethodPost {
		s.importTasks(w, r)
		return
	}
	if id == "tags" && len(parts) == 1 && r.Method == http.MethodGet {
		s.handleTagsInUse(w, r)
		return
	}
	if id == "tags" && len(parts) == 2 && r.Method == http.MethodPost {
		s.handleTagEdit(w, r, parts[1])
		return
	}
	if id == "batch" && len(parts) == 1 && r.Method == http.MethodPost {
		s.handleTaskBatch(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		s.updateTask(w, r, id)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if _, err := s.deps.Tasks.Get(ctx, id); err != nil {
			writeErr(w, 404, "task not found")
			return
		}
		// soft delete: history, versions and spend stay for the audit trail
		if err := s.deps.Tasks.SetStatus(ctx, id, "archived", "deleted in WebUI"); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		_ = s.deps.Tasks.SetSchedule(ctx, id, "")
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "duplicate" && r.Method == http.MethodPost {
		s.duplicateTask(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "form" {
		f, err := s.taskFormOf(ctx, id)
		if err != nil {
			writeErr(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, f)
		return
	}
	if len(parts) == 1 {
		v, err := s.deps.Tasks.Get(ctx, id)
		if err != nil {
			writeErr(w, 404, "task not found")
			return
		}
		d := toTaskDTO(v)
		d.Workspace, d.WorkspaceDefault = s.taskWorkspace(d.ID, d.Workspace)
		if m, err := s.taskTags(ctx, id); err == nil && m[id] != nil {
			d.Tags = m[id]
		}
		writeJSON(w, 200, d)
		return
	}
	switch parts[1] {
	case "amendments-confirm":
		// Confirm selected pending items across messages of this task in one go: one new version.
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil || len(body.IDs) == 0 || len(body.IDs) > 100 {
				writeErr(w, 400, "ids required (1-100)")
				return
			}
			for _, iid := range body.IDs {
				var tid string
				if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT task_id FROM requirement_items WHERE id=?`, iid).Scan(&tid); err != nil || tid != id {
					writeErr(w, 400, "item does not belong to this task")
					return
				}
			}
			res, err := s.deps.Amend.ConfirmItems(r.Context(), body.IDs, "user")
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, res)
		})).ServeHTTP(w, r)
		return
	case "amendments":
		if r.Method == http.MethodGet {
			list, err := s.deps.Amend.List(ctx, id, "")
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, list)
			return
		}
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Utterance string `json:"utterance"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Utterance) == "" {
				writeErr(w, 400, "utterance required")
				return
			}
			items, err := s.deps.Amend.Propose(r.Context(), id, body.Utterance, "user")
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, items)
		})).ServeHTTP(w, r)
		return
	case "versions":
		vers, err := s.deps.Ver.ListVersions(ctx, version.EntityTask, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		cur, _ := s.deps.Ver.GetPointer(ctx, version.EntityTask, id, version.PointerCurrent)
		out := []versionDTO{}
		for _, v := range vers {
			out = append(out, versionDTO{ID: v.ID, No: v.VersionNo, ParentID: v.ParentVersionID.String,
				Source: v.Source, Message: v.Message.String, CreatedAt: v.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"versions": out, "current": cur.String})
		return
	case "runs":
		runs, err := s.queryRuns(ctx, "WHERE r.task_id=? ORDER BY r.created_at DESC LIMIT ?", id, limitParam(r, 10, 100))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, runs)
		return
	case "run":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || s.deps.RunTask == nil {
				writeErr(w, 405, "method")
				return
			}
			runID, err := s.deps.RunTask(id, cost.BucketProduction)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			s.writeStarted(w, runID)
		})).ServeHTTP(w, r)
		return
	case "pause", "resume":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := "paused"
			if parts[1] == "resume" {
				st = "active"
			}
			if err := s.deps.Tasks.SetStatus(r.Context(), id, st, "webui"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "status": st})
		})).ServeHTTP(w, r)
		return
	case "rollback":
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				VersionID string `json:"version_id"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
			if body.VersionID == "" {
				writeErr(w, 400, "version_id required")
				return
			}
			if err := s.deps.Tasks.Rollback(r.Context(), id, body.VersionID, "user", "webui rollback"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		})).ServeHTTP(w, r)
		return
	}
	writeErr(w, 404, "not found")
}

func (s *Server) handleAmendAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/amendments/"), "/")
	if len(parts) < 2 || r.Method != http.MethodPost {
		writeErr(w, 404, "not found")
		return
	}
	id, action := parts[0], parts[1]
	var bb struct {
		Only []string `json:"only"`
	}
	switch action {
	case "confirm-batch":
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&bb)
		res, err := s.deps.Amend.ConfirmBatch(r.Context(), id, bb.Only, "user")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)
	case "reject-batch":
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&bb)
		n, err := s.deps.Amend.RejectBatch(r.Context(), id, bb.Only)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"rejected": n})
	case "confirm":
		a, err := s.deps.Amend.Confirm(r.Context(), id, "user")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, a)
	case "edit":
		var body struct {
			Content string `json:"content"`
			Scope   string `json:"scope"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body)
		it, err := s.deps.Amend.Edit(r.Context(), id, body.Content, body.Scope)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, it)
	case "reject":
		if err := s.deps.Amend.Reject(r.Context(), id); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "resolve":
		var body struct {
			Resolution string `json:"resolution"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body)
		if err := s.deps.Amend.ResolveConflict(r.Context(), id, body.Resolution); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 404, "not found")
	}
}
