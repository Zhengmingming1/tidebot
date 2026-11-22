package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// missingWorkspaceNoticeKey is set by agentd restore so the WebUI can tell the
// owner once about tasks whose working folders are not on this computer.
// User-selected folders live outside the tidebot home and are not rewritten
// (version content is immutable and content-addressed).
const missingWorkspaceNoticeKey = "restore.missing_workspaces"

const maxMissingWorkspaceTasks = 200

// MissingWorkspaceTask is one live task whose current working folder is missing
// after a restore (the path a run would actually use).
type MissingWorkspaceTask struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	WorkspaceDir string `json:"workspace_dir"`
	Status       string `json:"status,omitempty"`
}

// MissingWorkspaceWorkflow is one workflow whose working folder (the current
// definition's workspace_dir) is missing after a restore. Workflows have no
// per-run default folder, so a run would be refused until the owner picks one.
type MissingWorkspaceWorkflow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	WorkspaceDir string `json:"workspace_dir"`
}

type missingWorkspaceNoticeState struct {
	State string `json:"state"`
}

// MarkMissingWorkspaceNotice records that a backup has just been restored, so
// the next signed-in session can list tasks whose folders are missing. A later
// restore shows the notice again even if an earlier one was dismissed.
func MarkMissingWorkspaceNotice(ctx context.Context, db *sql.DB) error {
	return setMissingWorkspaceNoticeState(ctx, db, "pending")
}

// DismissMissingWorkspaceNotice hides the notice for good (until the next restore).
func DismissMissingWorkspaceNotice(ctx context.Context, db *sql.DB) error {
	return setMissingWorkspaceNoticeState(ctx, db, "dismissed")
}

func setMissingWorkspaceNoticeState(ctx context.Context, db *sql.DB, state string) error {
	if db == nil {
		return errors.New("database not open")
	}
	raw, _ := json.Marshal(missingWorkspaceNoticeState{State: state})
	_, err := db.ExecContext(ctx, `INSERT INTO settings(user_id,key,value_json,updated_at) VALUES('owner',?,?,?)
ON CONFLICT(user_id,key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`,
		missingWorkspaceNoticeKey, string(raw), time.Now().UTC().Format(time.RFC3339))
	return err
}

func missingWorkspaceNoticePending(ctx context.Context, db *sql.DB) (bool, error) {
	if db == nil {
		return false, nil
	}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE user_id='owner' AND key=?`, missingWorkspaceNoticeKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var v missingWorkspaceNoticeState
	if json.Unmarshal([]byte(raw), &v) != nil {
		return false, nil
	}
	return v.State == "pending", nil
}

// handleMissingWorkspaces: GET /api/tasks/missing-workspaces → {pending, tasks, workflows};
// POST dismisses the notice. When a restore is pending but every current folder
// exists (or is the per-task default under the tidebot home), GET clears the
// flag and returns pending=false so the dialog is not shown.
func (s *Server) handleMissingWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getMissingWorkspaces(w, r)
	case http.MethodPost:
		if s.deps.DB == nil {
			writeErr(w, 500, "database not open")
			return
		}
		if err := DismissMissingWorkspaceNotice(r.Context(), s.deps.DB); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, noMissingWorkspaces)
	default:
		writeErr(w, 405, "GET or POST only")
	}
}

func (s *Server) getMissingWorkspaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, err := missingWorkspaceNoticePending(ctx, s.deps.DB)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !pending {
		writeJSON(w, 200, noMissingWorkspaces)
		return
	}
	tasks, err := s.listMissingTaskWorkspaces(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	flows, err := s.listMissingWorkflowWorkspaces(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(tasks) == 0 && len(flows) == 0 {
		if err := DismissMissingWorkspaceNotice(ctx, s.deps.DB); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, noMissingWorkspaces)
		return
	}
	writeJSON(w, 200, map[string]any{"pending": true, "tasks": tasks, "workflows": flows})
}

var noMissingWorkspaces = map[string]any{"pending": false, "tasks": []MissingWorkspaceTask{}, "workflows": []MissingWorkspaceWorkflow{}}

// folderMissing reports whether a saved working folder is absent on this
// computer, expanding "~" the same way a run does.
func folderMissing(ws string) bool {
	fi, err := os.Stat(guard.ExpandHome(ws))
	return err != nil || !fi.IsDir()
}

func (s *Server) listMissingTaskWorkspaces(ctx context.Context) ([]MissingWorkspaceTask, error) {
	if s.deps.Tasks == nil {
		return nil, nil
	}
	list, err := s.deps.Tasks.List(ctx)
	if err != nil {
		return nil, err
	}
	home := ""
	if s.deps.Cfg != nil {
		home = s.deps.Cfg.HomeDir
	}
	out := make([]MissingWorkspaceTask, 0)
	for _, v := range list {
		if v.Task.Status == "archived" {
			continue
		}
		ws := v.Task.WorkspaceDir
		if v.Spec != nil && v.Spec.WorkspaceDir != "" {
			ws = v.Spec.WorkspaceDir
		}
		if ws == "" {
			continue
		}
		// Default folders under the (already remapped) tidebot home are created
		// on the first run; they are not the user's own project folder.
		if home != "" && within(guard.ExpandHome(ws), home) {
			continue
		}
		if !folderMissing(ws) {
			continue
		}
		out = append(out, MissingWorkspaceTask{ID: v.Task.ID, Name: v.Task.Name, WorkspaceDir: ws, Status: v.Task.Status})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxMissingWorkspaceTasks {
		out = out[:maxMissingWorkspaceTasks]
	}
	return out, nil
}

// listMissingWorkflowWorkspaces lists live workflows whose current definition
// names a folder that is not on this computer. Definitions are versioned and
// left as they are; the owner edits the workflow to pick a new folder.
func (s *Server) listMissingWorkflowWorkspaces(ctx context.Context) ([]MissingWorkspaceWorkflow, error) {
	out := make([]MissingWorkspaceWorkflow, 0)
	if s.deps.Workflows == nil {
		return out, nil
	}
	list, err := s.deps.Workflows.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, wf := range list {
		ws := wf.Definition.WorkspaceDir
		if ws == "" || !folderMissing(ws) {
			continue
		}
		out = append(out, MissingWorkspaceWorkflow{ID: wf.ID, Name: wf.Name, WorkspaceDir: ws})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxMissingWorkspaceTasks {
		out = out[:maxMissingWorkspaceTasks]
	}
	return out, nil
}
