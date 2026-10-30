package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// DryRunBudgetUSD caps a sandbox run.
const DryRunBudgetUSD = 0.5

// dryRunMaxCopy limits how much of the workspace is copied into the sandbox.
const dryRunMaxCopy = 200 << 20

// DryRun is a sandboxed trial of a task: the workspace is copied to a temp dir, the run is
// billed to the dry_run bucket, side-effect plugin connectors are removed, and the diff
// against the real workspace is reported. The real workspace is never touched.
type DryRun struct {
	ID       string   `json:"id"`
	TaskID   string   `json:"task_id"`
	Status   string   `json:"status"` // running | done | failed
	RunID    string   `json:"run_id,omitempty"`
	CostUSD  float64  `json:"cost_usd"`
	Result   string   `json:"result,omitempty"`
	Error    string   `json:"error,omitempty"`
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Deleted  []string `json:"deleted"`
}

var dryRuns = struct {
	sync.Mutex
	m map[string]*DryRun
}{m: map[string]*DryRun{}}

// StartDryRun launches a sandbox run in the background.
func (a *App) StartDryRun(taskID string) (*DryRun, error) {
	ctx := context.Background()
	v, err := a.Tasks.Get(ctx, taskID)
	if err != nil {
		return nil, errors.New("task not found")
	}
	prompt, versionID, _, err := a.Tasks.CompilePrompt(ctx, taskID)
	if err != nil {
		return nil, err
	}
	src := v.Task.WorkspaceDir
	if v.Spec != nil && v.Spec.WorkspaceDir != "" {
		src = v.Spec.WorkspaceDir
	}
	if src == "" {
		src = guard.TaskWorkspace(a.Cfg.HomeDir, taskID)
	}
	box, err := os.MkdirTemp("", "tidebot-dryrun-")
	if err != nil {
		return nil, err
	}
	before, err := copyTree(src, box, dryRunMaxCopy)
	if err != nil {
		os.RemoveAll(box)
		return nil, err
	}
	var st *skillStage
	if v.Spec != nil && len(v.Spec.SkillBindings) > 0 {
		if st, err = a.stageSkills(ctx, v.Spec.SkillBindings); err != nil {
			a.Log.Warn("stage skills", "task_id", taskID, "error", err)
		}
	}
	dr := &DryRun{ID: id.New("dry"), TaskID: taskID, Status: "running"}
	dryRuns.Lock()
	dryRuns.m[dr.ID] = dr
	dryRuns.Unlock()
	go func() {
		defer os.RemoveAll(box)
		defer st.Close()
		res, err := a.RunPromptEx(ctx, runner.Options{Prompt: prompt, WorkspaceDir: box, TriggerKind: "dry_run", TaskID: taskID,
			TaskVersionID: versionID, CostBucket: cost.BucketDryRun, ContextArgs: st.Args(a.modernClaude(), false)}, "", 15*time.Minute)
		after := hashTree(box)
		dryRuns.Lock()
		defer dryRuns.Unlock()
		dr.RunID, dr.CostUSD, dr.Result = res.RunID, a.RunCostUSD(res), res.ResultText
		dr.Added, dr.Modified, dr.Deleted = diffTrees(before, after)
		dr.Status = "done"
		if err != nil {
			dr.Status, dr.Error = "failed", err.Error()
		}
	}()
	return dr, nil
}

// DryRunResult returns a sandbox run's state.
func (a *App) DryRunResult(drID string) (*DryRun, error) {
	dryRuns.Lock()
	defer dryRuns.Unlock()
	dr, ok := dryRuns.m[drID]
	if !ok {
		return nil, errors.New("dry run not found")
	}
	cp := *dr
	return &cp, nil
}

func copyTree(src, dst string, max int64) (map[string]string, error) {
	var total int64
	if _, err := os.Stat(src); err != nil {
		return map[string]string{}, nil
	}
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if fi.IsDir() && fi.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		total += fi.Size()
		if total > max {
			return errors.New("workspace too large for a dry run (200 MB)")
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		return nil, err
	}
	return hashTree(dst), nil
}

func hashTree(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		h := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return nil
	})
	return out
}

func diffTrees(before, after map[string]string) (added, modified, deleted []string) {
	added, modified, deleted = []string{}, []string{}, []string{}
	for p, h := range after {
		if b, ok := before[p]; !ok {
			added = append(added, p)
		} else if b != h {
			modified = append(modified, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)
	return
}
