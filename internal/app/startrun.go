package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// readOnlyDeny are the tools a read-only run never gets. --disallowedTools wins
// over any allow (a skill's allowed-tools included). Runs are --restricted too,
// which already drops Bash and the other code-running tools; listing them keeps
// the rule if that ever changes. The guard hook is not relied on: --bare runs
// skip hooks.
var readOnlyDeny = []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Bash", "PowerShell"}

// editDirs: folders with a run that may change files going (one at a time).
var editDirs = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

func lockEditDir(dir string) bool {
	k := dir
	if caseFoldPaths {
		k = strings.ToLower(k)
	}
	editDirs.Lock()
	defer editDirs.Unlock()
	if editDirs.m[k] {
		return false
	}
	editDirs.m[k] = true
	return true
}

func unlockEditDir(dir string) {
	k := dir
	if caseFoldPaths {
		k = strings.ToLower(k)
	}
	editDirs.Lock()
	delete(editDirs.m, k)
	editDirs.Unlock()
}

// startRun starts a Run dialog run: the prompt, with the skill (if any) staged in
// tidebot's own folder and invoked as its slash command, in the chosen working
// folder (if any). With a folder the run is read-only unless Mode is "edit"; a
// folder must pass guard.CheckRunDir (symlinks resolved, sensitive folders
// refused), and only one run that may change files goes in a folder at a time.
func (a *App) startRun(req webui.RunRequest) (string, error) {
	ctx := context.Background()
	if strings.TrimSpace(req.Prompt) == "" {
		return "", errors.New("prompt required")
	}
	if _, err := a.ResolveProfile(req.Provider); err != nil {
		return "", err
	}
	dir, mode := "", ""
	if ws := guard.ExpandHome(req.Workspace); ws != "" {
		home, _ := os.UserHomeDir()
		real, prob := guard.CheckRunDir(ws, home, a.Cfg.HomeDir)
		if prob != "" {
			return "", &guard.DirError{Problem: prob}
		}
		dir, mode = real, "read"
		if req.Mode == "edit" {
			mode = "edit"
		}
	}
	if req.SkillID == "" && dir == "" {
		return a.startQuickRunAsync(req.Prompt, req.Provider)
	}
	var st *skillStage
	versionID := ""
	if req.SkillID != "" {
		var n int
		if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE id=?`, req.SkillID).Scan(&n); err != nil || n == 0 {
			return "", webui.ErrUnknownSkill
		}
		b := tasksvc.SkillBinding{SkillID: req.SkillID, Mode: "follow"}
		if req.VersionID != "" {
			var owner string
			err := a.DB.QueryRowContext(ctx, `SELECT entity_id FROM versions WHERE id=? AND entity_type='skill'`, req.VersionID).Scan(&owner)
			if errors.Is(err, sql.ErrNoRows) || owner != req.SkillID {
				return "", webui.ErrUnknownSkill
			}
			b = tasksvc.SkillBinding{SkillID: req.SkillID, Mode: "pinned", VersionID: req.VersionID}
		}
		var err error
		if st, err = a.stageSkills(ctx, []tasksvc.SkillBinding{b}); err != nil {
			return "", fmt.Errorf("skill: %w", err)
		}
		versionID = st.Skills[0].VersionID
	}
	if dir == "" { // a skill on its own: an empty scratch folder in the stage
		dir = filepath.Join(st.Dir, "work")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			st.Close()
			return "", err
		}
	}
	if mode == "edit" && !lockEditDir(dir) {
		st.Close()
		return "", guard.ErrDirBusy
	}
	modern := a.modernClaude()
	args := st.Args(modern, true)
	if mode != "edit" {
		args = append(args, "--disallowedTools", strings.Join(readOnlyDeny, ","))
	}
	return launch(startWait, func(onStart func(string)) error {
		defer st.Close()
		if mode == "edit" {
			defer unlockEditDir(dir)
		}
		_, err := a.RunPromptEx(ctx, runner.Options{
			Prompt: st.Invoke(modern, req.Prompt), DisplayPrompt: req.Prompt, WorkspaceDir: dir,
			TriggerKind: "quick", CostBucket: cost.BucketProduction, OnStart: onStart, ContextArgs: args,
			SkillID: req.SkillID, SkillVersionID: versionID, RunMode: mode,
		}, req.Provider, 0)
		return err
	}, func(err error) { a.Log.Warn("run", "skill_id", req.SkillID, "error", err) })
}

var caseFoldPaths = runtime.GOOS == "darwin" || runtime.GOOS == "windows"
