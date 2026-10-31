package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// runSnapRoot holds the scratch snapshots of runs that may change their folder
// (see gitsnap): never inside the folder or its .git.
func (a *App) runSnapRoot() string { return filepath.Join(a.Cfg.DataDir, "runsnap") }

// purgeRunSnaps removes snapshots a crashed core left behind (startup only).
func (a *App) purgeRunSnaps() {
	ents, err := os.ReadDir(a.runSnapRoot())
	if err != nil {
		return
	}
	for _, e := range ents {
		_ = os.RemoveAll(filepath.Join(a.runSnapRoot(), e.Name()))
	}
}

// changeTracker records what an edit run changed in a git work tree: a snapshot
// right before Claude Code starts (after any wait for a slot), another once it
// exits, the difference saved on the run row before the run is marked done.
// Not a git repo, git missing or a snapshot failing never stops the run.
type changeTracker struct {
	a     *App
	dir   string
	mu    sync.Mutex
	runID string
	snap  *gitsnap.Snap
}

func (t *changeTracker) onStart(next func(string)) func(string) {
	return func(id string) {
		t.mu.Lock()
		t.runID = id
		t.mu.Unlock()
		if next != nil {
			next(id)
		}
	}
}

// atStart is the run's AtStart: take the "before" snapshot.
func (t *changeTracker) atStart(ctx context.Context, _ *runner.Options) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runID == "" || t.snap != nil {
		return nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	begin := time.Now()
	s, err := gitsnap.Start(sctx, t.dir, filepath.Join(t.a.runSnapRoot(), t.runID))
	if err != nil {
		if !errors.Is(err, gitsnap.ErrNotRepo) {
			t.a.Log.Warn("run changes: snapshot", "run_id", t.runID, "error", err)
		}
		return nil
	}
	t.snap = s
	t.a.Log.Debug("run changes: snapshot", "run_id", t.runID, "ms", time.Since(begin).Milliseconds())
	return nil
}

// afterExit is the run's AfterExit: diff against the start, save, clean up.
func (t *changeTracker) afterExit(ctx context.Context, runID string) string {
	t.mu.Lock()
	s := t.snap
	t.snap = nil
	t.mu.Unlock()
	if s == nil {
		return ""
	}
	defer s.Close()
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c, err := s.Finish(sctx)
	if err != nil {
		t.a.Log.Warn("run changes: diff", "run_id", runID, "error", err)
		return ""
	}
	b, _ := json.Marshal(c)
	wctx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if _, err := t.a.DB.ExecContext(wctx, `UPDATE runs SET file_changes=?, file_patch=? WHERE id=?`, string(b), c.Patch, runID); err != nil {
		t.a.Log.Warn("run changes: save", "run_id", runID, "error", err)
		return ""
	}
	if c.Count == 0 {
		return "files changed: none"
	}
	return fmt.Sprintf("files changed: %d (+%d -%d)", c.Count, c.Add, c.Del)
}

// close drops a snapshot whose run never got to exit (skipped, refused).
func (t *changeTracker) close() {
	t.mu.Lock()
	s := t.snap
	t.snap = nil
	t.mu.Unlock()
	s.Close()
}
