package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// undoRunChanges takes an edit run's recorded patch back out of its folder (redo:
// applies it again) and returns the new changes_undone_at (” after redo).
//
// Unlike a checkpoint restore (cline/cline sdk/packages/core/src/session/
// checkpoint-restore.ts resets the work tree to the checkpoint with `reset --hard`
// + `clean -fd`, so it has to stash everything first in case it must roll back),
// only this run's own hunks are reversed: edits made before or after the run in
// other places stay, and git apply checks every hunk before writing anything, so a
// file changed since the run makes the whole undo refuse and nothing is touched.
// The folder is checked again (sensitive folders refused) and held like an edit run.
func (a *App) undoRunChanges(ctx context.Context, runID string, redo bool) (string, error) {
	var mode, status, dir, fc, patch, undoneAt string
	err := a.DB.QueryRowContext(ctx, `SELECT run_mode, status, workspace_dir, file_changes, file_patch, changes_undone_at FROM runs WHERE id=?`, runID).
		Scan(&mode, &status, &dir, &fc, &patch, &undoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", webui.ErrRunNotFound
	}
	if err != nil {
		return "", err
	}
	var c struct {
		Count    int  `json:"count"`
		PatchCut bool `json:"patch_cut"`
	}
	if mode != "edit" || fc == "" || json.Unmarshal([]byte(fc), &c) != nil || c.Count == 0 || patch == "" {
		return "", &webui.UndoError{Code: "none"}
	}
	switch status {
	case "succeeded", "failed", "canceled", "timeout", "skipped":
	default:
		return "", &webui.UndoError{Code: "running"}
	}
	if c.PatchCut {
		return "", &webui.UndoError{Code: "cut"}
	}
	// runs before patches carried binary data say only "Binary files … differ"
	if strings.HasPrefix(patch, "Binary files ") || strings.Contains(patch, "\nBinary files ") {
		return "", &webui.UndoError{Code: "binary"}
	}
	if redo == (undoneAt == "") {
		return "", &webui.UndoError{Code: "state"}
	}
	home, _ := os.UserHomeDir()
	real, prob := guard.CheckRunDir(dir, home, a.Cfg.HomeDir)
	if prob != "" {
		return "", &webui.UndoError{Code: "dir", Detail: prob}
	}
	if !lockEditDir(real) {
		return "", &webui.UndoError{Code: "busy"}
	}
	defer unlockEditDir(real)
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	if err := gitsnap.Apply(actx, real, patch, !redo); err != nil {
		var ce *gitsnap.ErrConflict
		if errors.As(err, &ce) {
			return "", &webui.UndoError{Code: "conflict", Detail: ce.Message}
		}
		return "", err
	}
	at := ""
	if !redo {
		at = time.Now().UTC().Format(time.RFC3339)
	}
	// the folder is already changed: save even if the request went away meanwhile
	wctx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if _, err := a.DB.ExecContext(wctx, `UPDATE runs SET changes_undone_at=? WHERE id=?`, at, runID); err != nil {
		return "", err
	}
	a.Log.Info("run changes", "run_id", runID, "undone", !redo, "files", c.Count)
	return at, nil
}
