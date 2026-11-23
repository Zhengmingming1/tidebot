package webui

import (
	"os"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// taskWsMissing: the folder of a task that has run before is gone; the next run
// creates an empty one in its place (allowed, but rarely what the owner wants
// after a move or a restore on another computer).
const taskWsMissing = "missing"

// applyTaskWorkspaceProblems marks list rows whose next run would be refused
// for its folder, or would start in a new empty folder because the one it used
// is gone. It only stats paths: nothing is created and no definition changes.
func (s *Server) applyTaskWorkspaceProblems(out []taskDTO) {
	home, _ := os.UserHomeDir()
	for i := range out {
		out[i].WorkspaceProblem = s.taskWorkspaceProblem(out[i], home)
	}
}

func (s *Server) taskWorkspaceProblem(d taskDTO, userHome string) string {
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
	if d.LastRunAt == "" {
		return "" // never run: a missing path is created on the first run, as saved
	}
	if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
		return taskWsMissing
	}
	return ""
}
