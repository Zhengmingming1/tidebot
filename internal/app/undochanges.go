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
//
// path set: only that file's sections (one file of the run). The state is the set of
// files taken back so far (runs.changes_undone_files); "Undo changes" for the whole run
// then takes back the rest, "Put back" for the whole run puts back every file, and once
// every file is back out the run counts as undone (changes_undone_at).
func (a *App) undoRunChanges(ctx context.Context, runID string, redo bool, path string) (webui.UndoResult, error) {
	var res webui.UndoResult
	var mode, status, dir, fc, patch, undoneAt, undoneFiles string
	err := a.DB.QueryRowContext(ctx, `SELECT run_mode, status, workspace_dir, file_changes, file_patch, changes_undone_at, changes_undone_files FROM runs WHERE id=?`, runID).
		Scan(&mode, &status, &dir, &fc, &patch, &undoneAt, &undoneFiles)
	if errors.Is(err, sql.ErrNoRows) {
		return res, webui.ErrRunNotFound
	}
	if err != nil {
		return res, err
	}
	var c struct {
		Files    []gitsnap.File `json:"files"`
		Count    int            `json:"count"`
		PatchCut bool           `json:"patch_cut"`
	}
	if mode != "edit" || fc == "" || json.Unmarshal([]byte(fc), &c) != nil || c.Count == 0 || patch == "" {
		return res, &webui.UndoError{Code: "none"}
	}
	switch status {
	case "succeeded", "failed", "canceled", "timeout", "skipped":
	default:
		return res, &webui.UndoError{Code: "running"}
	}
	if c.PatchCut {
		return res, &webui.UndoError{Code: "cut"}
	}
	all := make([]string, 0, len(c.Files))
	listed := map[string]bool{}
	for _, f := range c.Files {
		all = append(all, f.Path)
		listed[f.Path] = true
	}
	// files taken back so far: all of them once the whole run is undone
	done := map[string]bool{}
	var doneList []string
	if undoneAt != "" {
		doneList = append(doneList, all...)
	} else if undoneFiles != "" {
		_ = json.Unmarshal([]byte(undoneFiles), &doneList)
	}
	for _, p := range doneList {
		done[p] = true
	}
	var part string // the sections to apply
	switch {
	case path != "":
		if !listed[path] {
			return res, &webui.UndoError{Code: "file"}
		}
		if c.Count > len(c.Files) { // files past the list cap couldn't be tracked one by one
			return res, &webui.UndoError{Code: "many"}
		}
		if redo != done[path] {
			return res, &webui.UndoError{Code: "state"}
		}
		part = gitsnap.FilePatch(patch, []string{path})
		if part == "" {
			return res, &webui.UndoError{Code: "file"}
		}
	case redo:
		if len(done) == 0 {
			return res, &webui.UndoError{Code: "state"}
		}
		if undoneAt != "" {
			part = patch
		} else {
			part = gitsnap.FilePatch(patch, doneList)
		}
	default:
		if undoneAt != "" {
			return res, &webui.UndoError{Code: "state"}
		}
		part = gitsnap.PatchWithout(patch, doneList)
		if part == "" {
			return res, &webui.UndoError{Code: "state"}
		}
	}
	// runs before patches carried binary data say only "Binary files … differ"
	if strings.HasPrefix(part, "Binary files ") || strings.Contains(part, "\nBinary files ") {
		return res, &webui.UndoError{Code: "binary"}
	}
	home, _ := os.UserHomeDir()
	real, prob := guard.CheckRunDir(dir, home, a.Cfg.HomeDir)
	if prob != "" {
		return res, &webui.UndoError{Code: "dir", Detail: prob}
	}
	if !lockEditDir(real) {
		return res, &webui.UndoError{Code: "busy"}
	}
	defer unlockEditDir(real)
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	if err := gitsnap.Apply(actx, real, part, !redo); err != nil {
		var ce *gitsnap.ErrConflict
		if errors.As(err, &ce) {
			return res, &webui.UndoError{Code: "conflict", Detail: ce.Message}
		}
		return res, err
	}
	// the new set of files taken back
	switch {
	case path != "" && redo:
		delete(done, path)
	case path != "":
		done[path] = true
	case redo:
		done = map[string]bool{}
	default:
		for _, p := range all {
			done[p] = true
		}
	}
	whole := len(done) > 0 && c.Count == len(c.Files)
	for _, p := range all {
		if !done[p] {
			whole = false
			break
		}
	}
	if path == "" && !redo {
		whole = true // files past the list cap went back with the rest
	}
	files := ""
	if whole {
		res.UndoneAt = undoneAt
		if res.UndoneAt == "" {
			res.UndoneAt = time.Now().UTC().Format(time.RFC3339)
		}
	} else if len(done) > 0 {
		for _, p := range all { // keep the run's file order
			if done[p] {
				res.UndoneFiles = append(res.UndoneFiles, p)
			}
		}
		b, _ := json.Marshal(res.UndoneFiles)
		files = string(b)
	}
	// the folder is already changed: save even if the request went away meanwhile
	wctx, done2 := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done2()
	if _, err := a.DB.ExecContext(wctx, `UPDATE runs SET changes_undone_at=?, changes_undone_files=? WHERE id=?`, res.UndoneAt, files, runID); err != nil {
		return res, err
	}
	a.Log.Info("run changes", "run_id", runID, "undone", !redo, "one_file", path != "", "files_undone", len(done))
	return res, nil
}
