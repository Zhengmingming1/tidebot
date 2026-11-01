package app

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// Limits of one undo check: per-file checks only run when a group's combined
// check fails (to name the files), at most this many, within this time.
const (
	undoCheckMaxFiles = 200
	undoCheckBudget   = 8 * time.Second
)

// checkRunUndo answers, before anyone clicks, which of the run page's undo buttons
// would work now: `git apply --check` (nothing written, no lock taken) of the run's own
// sections against the folder as it is. The main button's action (undo the rest, or put
// everything back for a wholly undone run) goes in Whole; every listed file gets its own
// action's answer (undo it, or put it back when taken back already). Each group is
// checked in one go first; only when that fails is each file checked to name the ones
// edited since. Files past the budget are left out (unknown: the button stays on and
// the real undo still checks everything first).
//
// Idea: cline/cline apps/vscode/webview-ui/src/components/chat/UserMessage.tsx with
// src/core/controller/checkpoints/checkpointExistsForMessage.ts — whether "Restore" can
// work is asked when the user gets to it (pending → available / unavailable with the
// reason in the tooltip), not trusted from what was true when the turn ended.
func (a *App) checkRunUndo(ctx context.Context, runID string) (webui.UndoCheck, error) {
	out := webui.UndoCheck{Files: map[string]webui.UndoFileCheck{}}
	st, err := a.loadUndo(ctx, runID)
	if err != nil {
		var ue *webui.UndoError
		if errors.As(err, &ue) {
			out.Code = ue.Code
			return out, nil
		}
		return out, err
	}
	if hasBinaryStub(st.patch) {
		out.Code = "binary"
		return out, nil
	}
	home, _ := os.UserHomeDir()
	real, prob := guard.CheckRunDir(st.dir, home, a.Cfg.HomeDir)
	if prob != "" {
		out.Code = "dir"
		return out, nil
	}
	if editDirBusy(real) {
		out.Code = "busy"
		return out, nil
	}
	cctx, cancel := context.WithTimeout(ctx, undoCheckBudget+5*time.Second)
	defer cancel()
	deadline := time.Now().Add(undoCheckBudget)
	perFile := st.c.Count == len(st.c.Files) // files past the list cap can't be undone one by one
	singles := 0
	var undo, redo []string // files whose own action is undo / put back
	for _, p := range st.all {
		if st.done[p] {
			redo = append(redo, p)
		} else {
			undo = append(undo, p)
		}
	}
	// group: one combined check, then file by file only when it fails
	group := func(paths []string, part string, reverse bool) (bool, []string) {
		if part == "" {
			return true, nil
		}
		err := gitsnap.Check(cctx, real, part, reverse)
		if err == nil {
			for _, p := range paths {
				out.Files[p] = webui.UndoFileCheck{OK: true}
			}
			return true, nil
		}
		var ce *gitsnap.ErrConflict
		if !errors.As(err, &ce) {
			return true, nil // git missing / timed out: say nothing rather than block
		}
		var bad []string
		for _, p := range paths {
			if singles >= undoCheckMaxFiles || time.Now().After(deadline) {
				break
			}
			singles++
			ferr := gitsnap.Check(cctx, real, gitsnap.FilePatch(st.patch, []string{p}), reverse)
			if errors.As(ferr, &ce) {
				out.Files[p] = webui.UndoFileCheck{Detail: ce.Message}
				bad = append(bad, p)
			} else if ferr == nil {
				out.Files[p] = webui.UndoFileCheck{OK: true}
			}
		}
		return false, bad
	}
	if st.undoneAt != "" {
		// wholly undone: the main button puts the whole patch back (files past the cap too)
		ok, bad := group(st.all, st.patch, false)
		out.Whole, out.WholeFiles = wholeWord(ok), bad
	} else {
		ok, bad := group(undo, gitsnap.PatchWithout(st.patch, st.doneList), true)
		out.Whole, out.WholeFiles = wholeWord(ok), bad
		if len(redo) > 0 {
			group(redo, gitsnap.FilePatch(st.patch, redo), false)
		}
	}
	if !perFile {
		out.Files = map[string]webui.UndoFileCheck{}
	}
	return out, nil
}

func wholeWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "conflict"
}

// editDirBusy: an edit run (or an undo) holds dir right now.
func editDirBusy(dir string) bool {
	if !lockEditDir(dir) {
		return true
	}
	unlockEditDir(dir)
	return false
}
