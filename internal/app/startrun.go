package app

import (
	"context"
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
// folder (if any). With a folder the run is read-only unless Mode is "edit" (may
// change files) or "exec" (may also run commands, inside the system sandbox: see
// execsandbox.go); "exec" without a folder works in a scratch folder of tidebot's. A
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
		if req.Mode == "edit" || req.Mode == "exec" {
			mode = req.Mode
		}
	} else if req.Mode == "exec" {
		mode = "exec"
	}
	bindings := req.SkillBindings
	if bindings == nil && req.SkillID != "" {
		mode := "follow"
		if req.VersionID != "" {
			mode = "pinned"
		}
		bindings = []tasksvc.SkillBinding{{SkillID: req.SkillID, Mode: mode, VersionID: req.VersionID}}
	}
	if err := a.Skills.ValidateSetSource(ctx, req.SkillSetSource); err != nil {
		return "", err
	}
	if req.SkillSetSource != nil && len(bindings) == 0 {
		return "", errors.New("composition source requires skill bindings")
	}
	if len(bindings) == 0 && dir == "" && mode != "exec" {
		return a.startQuickRunAsync(req.Prompt, req.Provider)
	}
	var st *skillStage
	versionID := ""
	if len(bindings) > 0 {
		frozen, err := a.Skills.ResolveBindings(ctx, bindings)
		if err != nil {
			return "", fmt.Errorf("%w: %v", webui.ErrUnknownSkill, err)
		}
		st, err = a.stageSkills(ctx, frozen)
		if err != nil {
			return "", fmt.Errorf("skill: %w", err)
		}
		req.SkillID = frozen[0].SkillID
		versionID = frozen[0].VersionID
	}
	scratch := ""
	if dir == "" && st != nil { // a skill on its own: an empty scratch folder in the stage
		dir = filepath.Join(st.Dir, "work")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			st.Close()
			return "", err
		}
	} else if dir == "" { // a command run with no folder and no skill: its own scratch folder
		d, err := os.MkdirTemp(a.runWorkDir(), "run-")
		if err != nil {
			return "", err
		}
		dir, scratch = d, d
	}
	// a run that may change the owner's folder: one at a time per folder, changes tracked
	writes := mode == "edit" || mode == "exec" && scratch == "" && guard.ExpandHome(req.Workspace) != ""
	if writes && !lockEditDir(dir) {
		if scratch != "" {
			_ = os.RemoveAll(scratch)
		}
		st.Close()
		return "", guard.ErrDirBusy
	}
	modern := a.modernClaude()
	args, err := st.CheckedArgs(modern, true)
	if err != nil {
		if writes {
			unlockEditDir(dir)
		}
		if scratch != "" {
			_ = os.RemoveAll(scratch)
		}
		st.Close()
		return "", err
	}
	if mode != "edit" && mode != "exec" {
		args = append(args, "--disallowedTools", strings.Join(readOnlyDeny, ","))
	}
	prompt := st.Invoke(modern, req.Prompt)
	return launch(startWait, func(onStart func(string)) error {
		defer st.Close()
		if scratch != "" {
			defer os.RemoveAll(scratch)
		}
		opts := runner.Options{
			Prompt: prompt, DisplayPrompt: req.Prompt, WorkspaceDir: dir,
			TriggerKind: "quick", CostBucket: cost.BucketProduction, OnStart: a.skillStart(st, a.sourceStart(req.SkillSetSource, bindings, onStart)), ContextArgs: args, ContextRequest: st.contextRequest(modern, true),
			SkillID: req.SkillID, SkillVersionID: versionID, RunMode: mode,
		}
		if writes {
			defer unlockEditDir(dir)
			// what the run changed in the folder (git work trees), for the run page
			tr := &changeTracker{a: a, dir: dir}
			defer tr.close()
			opts.OnStart, opts.AtStart, opts.AfterExit = tr.onStart(a.skillStart(st, a.sourceStart(req.SkillSetSource, bindings, onStart))), tr.atStart, tr.afterExit
		}
		_, err := a.RunPromptEx(ctx, opts, req.Provider, 0)
		return err
	}, func(err error) { a.Log.Warn("run", "skill_id", req.SkillID, "error", err) })
}

var caseFoldPaths = runtime.GOOS == "darwin" || runtime.GOOS == "windows"
