package webui

import (
	"context"
	"os"
	"strconv"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// taskWsMissing: the folder of a task that has run before is gone; the next run
// creates an empty one in its place (allowed, but rarely what the owner wants
// after a move or a restore on another computer).
const taskWsMissing = "missing"

// applyTaskWorkspaceProblems marks list rows whose next run would be refused
// for its folder, or would start in a new empty folder because the one it used
// is gone. A workflow task carries the problem of the folder saved in the
// workflow version it runs (pinned, or the current one when it follows). It
// only stats paths: nothing is created and no definition changes.
func (s *Server) applyTaskWorkspaceProblems(ctx context.Context, out []taskDTO) {
	home, _ := os.UserHomeDir()
	seen := map[string][2]string{} // workflow id@version -> problem, folder (tasks often share one)
	for i := range out {
		ran := out[i].LastRunAt != ""
		s.applyTaskWorkspaceProblem(ctx, &out[i], home, seen, func() bool { return ran })
	}
}

// applyTaskWorkspaceProblem sets d's workspace problem; call it before the
// detail answer swaps an empty folder for the default one. seen may be nil.
// ran reports whether the task has run before (only asked for a gone folder).
func (s *Server) applyTaskWorkspaceProblem(ctx context.Context, d *taskDTO, userHome string, seen map[string][2]string, ran func() bool) {
	if d.Workflow == nil {
		d.WorkspaceProblem = s.taskWorkspaceProblem(*d, userHome, ran)
		return
	}
	key := d.Workflow.ID + "@" + strconv.Itoa(d.Workflow.Version)
	r, ok := seen[key]
	if !ok {
		r = s.taskWorkflowWorkspaceProblem(ctx, d.Workflow.ID, d.Workflow.Version)
		if seen != nil {
			seen[key] = r
		}
	}
	d.WorkspaceProblem, d.WorkflowWorkspace = r[0], r[1]
}

// taskWorkflowWorkspaceProblem: the folder problem a workflow task's next run
// would hit, from the definition of the version it runs (the same
// guard.CheckRunDir check the workflow run makes), and that folder. A deleted
// workflow or a missing version carries nothing here: the detail page already
// says the workflow is gone and the run reports its own error.
func (s *Server) taskWorkflowWorkspaceProblem(ctx context.Context, id string, version int) [2]string {
	if s.deps.Workflows == nil || id == "" {
		return [2]string{}
	}
	w, err := s.deps.Workflows.GetVersion(ctx, id, version)
	if err != nil {
		return [2]string{}
	}
	dir := w.Definition.WorkspaceDir
	if prob := s.workflowWorkspaceProblem(dir); prob != "" {
		return [2]string{prob, dir}
	}
	return [2]string{}
}

func (s *Server) taskWorkspaceProblem(d taskDTO, userHome string, ran func() bool) string {
	ws := d.Workspace
	if ws == "" || d.Workflow != nil {
		return ""
	}
	appHome := ""
	if cfg := s.deps.Cfg; cfg != nil && cfg.HomeDir != "" {
		appHome = cfg.HomeDir
		// tidebot's own default folders are created on the first run (the run
		// skips the owner-folder check for them too)
		if samePath(ws, cfg.DataDir) || samePath(ws, guard.TaskWorkspace(cfg.HomeDir, d.ID)) {
			return ""
		}
	}
	// the same check runTaskTriggerOn makes before it creates a run row
	if _, prob := guard.CheckPlannedDir(ws, userHome, appHome); prob != "" {
		return prob
	}
	if fi, err := os.Stat(ws); err == nil && fi.IsDir() {
		return ""
	}
	if !ran() {
		return "" // never run: a missing path is created on the first run, as saved
	}
	return taskWsMissing
}

// taskHasRun: whether any run row belongs to the task (the detail answer has
// no last_run_at). A failed query counts as not run, so nothing is flagged.
func (s *Server) taskHasRun(ctx context.Context, id string) bool {
	var n int
	if err := s.deps.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE task_id=?)`, id).Scan(&n); err != nil {
		return false
	}
	return n == 1
}
