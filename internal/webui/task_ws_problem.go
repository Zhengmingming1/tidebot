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
// workflow version it runs (pinned, or the current one when it follows), and
// its workspace becomes that folder, as on the detail answer: the runs never
// use the task's own. It only stats paths: nothing is created and no
// definition changes.
func (s *Server) applyTaskWorkspaceProblems(ctx context.Context, out []taskDTO) {
	home, _ := os.UserHomeDir()
	seen := map[string]taskWfFolder{} // workflow id@version -> its folder (tasks often share one)
	for i := range out {
		ran := out[i].LastRunAt != ""
		s.applyTaskWorkspaceProblem(ctx, &out[i], home, seen, func() bool { return ran })
		if d := &out[i]; d.Workflow != nil {
			d.Workspace, d.WorkspaceWorkflow = s.taskWorkflowFolder(ctx, d.Workflow.ID, d.Workflow.Version, seen).dir, true
		}
	}
}

// applyTaskWorkspaceProblem sets d's workspace problem; call it before the
// detail answer swaps an empty folder for the default one. seen may be nil.
// ran reports whether the task has run before (only asked for a gone folder).
func (s *Server) applyTaskWorkspaceProblem(ctx context.Context, d *taskDTO, userHome string, seen map[string]taskWfFolder, ran func() bool) {
	if d.Workflow == nil {
		d.WorkspaceProblem = s.taskWorkspaceProblem(*d, userHome, ran)
		return
	}
	f := s.taskWorkflowFolder(ctx, d.Workflow.ID, d.Workflow.Version, seen)
	d.WorkspaceProblem, d.WorkflowWorkspace = f.problem, f.problemDir
}

// taskWfFolder: what a workflow task's runs find at the folder of the workflow
// version they run. problem / problemDir are set only when a run would refuse
// the folder (problemDir as saved); dir is the folder with "~" expanded as the
// run does ("" = no folder: each run uses scratch folders, or the workflow or
// version is gone).
type taskWfFolder struct{ problem, problemDir, dir string }

// taskWorkflowFolder reads the version once per id@version (seen may be nil).
// A deleted workflow or a missing version carries nothing here: the detail
// page already says the workflow is gone and the run reports its own error.
func (s *Server) taskWorkflowFolder(ctx context.Context, id string, version int, seen map[string]taskWfFolder) taskWfFolder {
	key := id + "@" + strconv.Itoa(version)
	if f, ok := seen[key]; ok {
		return f
	}
	var f taskWfFolder
	if s.deps.Workflows != nil && id != "" {
		if w, err := s.deps.Workflows.GetVersion(ctx, id, version); err == nil {
			dir := w.Definition.WorkspaceDir
			f.dir = guard.ExpandHome(dir)
			// the same guard.CheckRunDir check the workflow run makes
			if prob := s.workflowWorkspaceProblem(dir); prob != "" {
				f.problem, f.problemDir = prob, dir
			}
		}
	}
	if seen != nil {
		seen[key] = f
	}
	return f
}

// taskWorkflowDir: the folder a workflow task's runs work in, from the
// definition of the version it runs (version 0 = the current one), with "~"
// expanded as the workflow run does. "" when the workflow names no folder (each
// run then uses scratch folders) or the workflow / version is gone.
func (s *Server) taskWorkflowDir(ctx context.Context, id string, version int) string {
	return s.taskWorkflowFolder(ctx, id, version, nil).dir
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
