package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// PackFormat identifies workflow pack zips.
const PackFormat = "tidebot-pack/1"

// Pack is pack.json inside a workflow pack zip. Skills live under skills/<name>/.
type Pack struct {
	Format    string     `json:"format"`
	Name      string     `json:"name"`
	CreatedAt string     `json:"created_at"`
	Tasks     []PackTask `json:"tasks"`
	Skills    []string   `json:"skills"`
}

// PackTask is a portable task: no IDs, no workspace paths, no provider secrets.
type PackTask struct {
	Name           string                   `json:"name"`
	Goal           string                   `json:"goal,omitempty"`
	Prompt         string                   `json:"prompt"`
	Constraints    []tasksvc.SpecConstraint `json:"constraints,omitempty"`
	CronExpr       string                   `json:"cron_expr,omitempty"`
	OverlapPolicy  string                   `json:"overlap_policy,omitempty"`
	TimeoutMinutes int                      `json:"timeout_minutes"`
	AllowedTools   []string                 `json:"allowed_tools,omitempty"`
	Skills         []string                 `json:"skills,omitempty"`
	// RunMode: read | edit | exec; "" = not recorded (a pack made before this
	// field, or a task saved before the three levels), which runs as edit did.
	// Effort: "" (Claude Code decides) | low | medium | high.
	RunMode string `json:"run_mode,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// ExportPack writes the given tasks and their resolved skill versions to a zip.
func (a *App) ExportPack(ctx context.Context, name string, taskIDs []string, out string) (string, error) {
	p := Pack{Format: PackFormat, Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	skillFiles := map[string]map[string][]byte{}
	versionByName := map[string]string{}
	for _, tid := range taskIDs {
		v, err := a.Tasks.Get(ctx, tid)
		if err != nil || v.Spec == nil {
			return "", fmt.Errorf("task %s not found", tid)
		}
		// Pack/1 has no place for a workflow: exported as a plain prompt it would
		// come back as a different task that never runs the workflow.
		if v.Spec.WorkflowID != "" {
			return "", fmt.Errorf("task %q runs a workflow, which a pack cannot carry; export it as a task file instead", v.Task.Name)
		}
		pt := PackTask{Name: v.Task.Name, Goal: v.Spec.Goal, Prompt: v.Spec.Prompt, Constraints: v.Spec.Constraints,
			CronExpr: v.Task.CronExpr, OverlapPolicy: v.Task.OverlapPolicy, TimeoutMinutes: v.Spec.TimeoutMinutes, AllowedTools: v.Spec.AllowedTools,
			RunMode: v.Spec.RunMode, Effort: v.Spec.Effort}
		bindings, err := a.Skills.ResolveBindings(ctx, v.Spec.SkillBindings)
		if err != nil {
			return "", fmt.Errorf("task %s: %w", tid, err)
		}
		for _, b := range bindings {
			sn, vid := b.SkillName, b.VersionID
			// Pack/1 stores one directory per skill name; it cannot represent two
			// versions of that name without silently changing a task's binding.
			if previous, ok := versionByName[sn]; ok && previous != vid {
				return "", fmt.Errorf("skill %q resolves to different versions in these tasks; export the tasks separately", sn)
			}
			versionByName[sn] = vid
			pt.Skills = append(pt.Skills, sn)
			if _, done := skillFiles[sn]; !done {
				files, err := a.Skills.Files(ctx, vid)
				if err != nil {
					return "", err
				}
				skillFiles[sn] = files
				p.Skills = append(p.Skills, sn)
			}
		}
		p.Tasks = append(p.Tasks, pt)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	pj, _ := json.MarshalIndent(p, "", "  ")
	w, _ := zw.Create("pack.json")
	_, _ = w.Write(pj)
	// Skills in first-use order, files by name: the same tasks give the same
	// entries in the same order (map order would shuffle them on every export).
	for _, sn := range p.Skills {
		files := skillFiles[sn]
		fns := make([]string, 0, len(files))
		for fn := range files {
			fns = append(fns, fn)
		}
		sort.Strings(fns)
		for _, fn := range fns {
			w, err := zw.Create(path.Join("skills", sn, filepath.ToSlash(fn)))
			if err != nil {
				return "", err
			}
			_, _ = w.Write(files[fn])
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	h := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(h[:]), nil
}

// PackImportResult reports what an import created.
type PackImportResult struct {
	Hash      string   `json:"hash"`
	Duplicate bool     `json:"duplicate"`
	TaskIDs   []string `json:"task_ids"`
	Skills    []string `json:"skills"`
}

const packMaxBytes = 32 << 20

// ImportPack imports a pack zip. The same zip (by sha256) is imported only once.
// Imported tasks start paused so the owner can review them before they run.
func (a *App) ImportPack(ctx context.Context, file string) (*PackImportResult, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return a.importPackBytes(ctx, raw)
}

// importPackBytes imports a pack zip already in memory (built-in packs are
// assembled in memory and need no temporary file).
func (a *App) importPackBytes(ctx context.Context, raw []byte) (*PackImportResult, error) {
	if len(raw) > packMaxBytes {
		return nil, errors.New("pack too large")
	}
	h := sha256.Sum256(raw)
	res := &PackImportResult{Hash: hex.EncodeToString(h[:])}
	if prev, err := a.Auth.GetSetting(ctx, "pack.imported."+res.Hash); err == nil && prev != "" {
		res.Duplicate = true
		_ = json.Unmarshal([]byte(prev), &res.TaskIDs)
		return res, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var p Pack
	skillFiles := map[string]map[string][]byte{}
	var total int64
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(f.Name, "\\") || f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe path in pack: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		// Two entries that clean to one path would silently replace each other.
		if seen[name] {
			return nil, fmt.Errorf("duplicate file in pack: %s", name)
		}
		seen[name] = true
		// A skill file is at most skillpkg.MaxFileBytes (as for any skill import);
		// refuse a bigger one instead of importing it cut short.
		if f.UncompressedSize64 > skillpkg.MaxFileBytes {
			return nil, fmt.Errorf("%s in pack is larger than %d MB", name, skillpkg.MaxFileBytes>>20)
		}
		total += int64(f.UncompressedSize64)
		if total > 4*packMaxBytes {
			return nil, errors.New("pack expands too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, skillpkg.MaxFileBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > skillpkg.MaxFileBytes {
			return nil, fmt.Errorf("%s in pack is larger than %d MB", name, skillpkg.MaxFileBytes>>20)
		}
		if name == "pack.json" {
			if err := json.Unmarshal(data, &p); err != nil {
				return nil, fmt.Errorf("pack.json: %w", err)
			}
			continue
		}
		parts := strings.SplitN(name, "/", 3)
		if len(parts) == 3 && parts[0] == "skills" {
			if skillFiles[parts[1]] == nil {
				skillFiles[parts[1]] = map[string][]byte{}
			}
			skillFiles[parts[1]][parts[2]] = data
		}
	}
	if p.Format != PackFormat {
		return nil, errors.New("not a tidebot workflow pack")
	}
	// Validate all references before importing any skill or creating a task. A
	// missing auxiliary skill must not turn a composition into a partial task.
	checkSkill := func(sn string) error {
		files, ok := skillFiles[sn]
		if !ok {
			return fmt.Errorf("pack is missing referenced skill %q", sn)
		}
		if _, upper := files["SKILL.md"]; !upper {
			if _, lower := files["skill.md"]; !lower {
				return fmt.Errorf("pack skill %q is missing SKILL.md", sn)
			}
		}
		return nil
	}
	for sn, files := range skillFiles {
		if err := checkSkill(sn); err != nil {
			return nil, err
		}
		// Same path, count and size rules as importing the skill's folder.
		if _, err := skillstore.CheckFiles(files); err != nil {
			return nil, fmt.Errorf("pack skill %q: %w", sn, err)
		}
	}
	for _, sn := range p.Skills {
		if err := checkSkill(sn); err != nil {
			return nil, err
		}
	}
	// Check every task the way Create and UpdateSpec will, so a bad task later
	// in the pack cannot leave the skills and earlier tasks imported without a
	// record of the pack (importing it again would make them twice).
	inputs := make([]tasksvc.CreateInput, len(p.Tasks))
	for i, task := range p.Tasks {
		if len(task.Skills) > tasksvc.MaxSkillBindings {
			return nil, fmt.Errorf("task %q: at most %d skills may be attached", task.Name, tasksvc.MaxSkillBindings)
		}
		attached := map[string]bool{}
		for _, sn := range task.Skills {
			if err := checkSkill(sn); err != nil {
				return nil, fmt.Errorf("task %q: %w", task.Name, err)
			}
			if attached[sn] {
				return nil, fmt.Errorf("task %q: skill %q is attached twice", task.Name, sn)
			}
			attached[sn] = true
		}
		// Created paused for review, with the reason, in one step: an imported
		// task is never active, not even if a later step fails.
		inputs[i] = tasksvc.CreateInput{Name: task.Name, Goal: task.Goal, Prompt: task.Prompt, CronExpr: task.CronExpr,
			OverlapPolicy: task.OverlapPolicy, TimeoutMinutes: task.TimeoutMinutes, Actor: "pack",
			InitialStatus: "paused", PauseKind: tasksvc.PausePack,
			// skills are attached after import; checked here: run mode and effort
			Spec: &tasksvc.Spec{RunMode: task.RunMode, Effort: task.Effort}}
		if err := a.Tasks.CheckCreate(inputs[i]); err != nil {
			return nil, fmt.Errorf("task %q: %w", task.Name, err)
		}
	}
	names := make([]string, 0, len(skillFiles))
	for sn := range skillFiles {
		names = append(names, sn)
	}
	sort.Strings(names)
	skillIDs := map[string]string{}
	for _, sn := range names {
		// A skill version is immutable and keyed by content: importing the same
		// files again reuses it, so a skill left by a failed import is harmless.
		sv, err := a.Skills.ImportFiles(ctx, sn, skillFiles[sn], "pack")
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", sn, err)
		}
		skillIDs[sn] = sv.ID
		res.Skills = append(res.Skills, sn)
	}
	// If a task still fails (database error), archive the ones this import
	// made so none is left behind half imported.
	undo := func() {
		for _, id := range res.TaskIDs {
			_ = a.Tasks.SetStatusWhy(context.WithoutCancel(ctx), id, "archived", "", "")
		}
	}
	for i, t := range p.Tasks {
		// its skills, constraints and tools are in version 1 itself: no v1
		// without them in the history to roll back to
		spec := tasksvc.Spec{Constraints: t.Constraints, AllowedTools: t.AllowedTools, RunMode: t.RunMode, Effort: t.Effort}
		for _, sn := range t.Skills {
			spec.SkillBindings = append(spec.SkillBindings, tasksvc.SkillBinding{SkillID: skillIDs[sn], SkillName: sn, Mode: "follow"})
		}
		in := inputs[i]
		in.Spec, in.Reason = &spec, "imported from pack "+p.Name
		tv, err := a.Tasks.Create(ctx, in)
		if err != nil {
			undo()
			return nil, fmt.Errorf("task %s: %w", t.Name, err)
		}
		res.TaskIDs = append(res.TaskIDs, tv.Task.ID)
	}
	ids, _ := json.Marshal(res.TaskIDs)
	_ = a.Auth.SetSetting(ctx, "pack.imported."+res.Hash, string(ids))
	return res, nil
}
