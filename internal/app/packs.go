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
	"strings"
	"time"

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
	TimeoutMinutes int                      `json:"timeout_minutes,omitempty"`
	AllowedTools   []string                 `json:"allowed_tools,omitempty"`
	Skills         []string                 `json:"skills,omitempty"`
}

// ExportPack writes the given tasks (and the current versions of their skills) to a zip.
func (a *App) ExportPack(ctx context.Context, name string, taskIDs []string, out string) (string, error) {
	p := Pack{Format: PackFormat, Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	skillFiles := map[string]map[string][]byte{}
	skills, _ := a.Skills.List(ctx)
	byID := map[string]string{}
	verOf := map[string]string{}
	for _, s := range skills {
		byID[s.ID] = s.Name
		if s.CurrentVersion != nil {
			verOf[s.ID] = s.CurrentVersion.ID
		}
	}
	for _, tid := range taskIDs {
		v, err := a.Tasks.Get(ctx, tid)
		if err != nil || v.Spec == nil {
			return "", fmt.Errorf("task %s not found", tid)
		}
		pt := PackTask{Name: v.Task.Name, Goal: v.Spec.Goal, Prompt: v.Spec.Prompt, Constraints: v.Spec.Constraints,
			CronExpr: v.Task.CronExpr, OverlapPolicy: v.Task.OverlapPolicy, TimeoutMinutes: v.Spec.TimeoutMinutes, AllowedTools: v.Spec.AllowedTools}
		for _, b := range v.Spec.SkillBindings {
			sn := byID[b.SkillID]
			vid := verOf[b.SkillID]
			if b.Mode == "pinned" && b.VersionID != "" {
				vid = b.VersionID
			}
			if sn == "" || vid == "" {
				continue
			}
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
	for sn, files := range skillFiles {
		for fn, data := range files {
			w, err := zw.Create(path.Join("skills", sn, filepath.ToSlash(fn)))
			if err != nil {
				return "", err
			}
			_, _ = w.Write(data)
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
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe path in pack: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > 4*packMaxBytes {
			return nil, errors.New("pack expands too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		rc.Close()
		if err != nil {
			return nil, err
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
	skillIDs := map[string]string{}
	for sn, files := range skillFiles {
		tmp, err := os.MkdirTemp("", "pack-skill-")
		if err != nil {
			return nil, err
		}
		for fn, data := range files {
			dst := filepath.Join(tmp, filepath.FromSlash(fn))
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			_ = os.WriteFile(dst, data, 0o644)
		}
		sv, err := a.Skills.ImportDir(ctx, sn, tmp, "pack")
		os.RemoveAll(tmp)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", sn, err)
		}
		skillIDs[sn] = sv.ID
		res.Skills = append(res.Skills, sn)
	}
	for _, t := range p.Tasks {
		tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: t.Name, Goal: t.Goal, Prompt: t.Prompt, CronExpr: t.CronExpr,
			OverlapPolicy: t.OverlapPolicy, TimeoutMinutes: t.TimeoutMinutes, Actor: "pack"})
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", t.Name, err)
		}
		spec := *tv.Spec
		spec.Constraints = t.Constraints
		spec.AllowedTools = t.AllowedTools
		for _, sn := range t.Skills {
			if sid := skillIDs[sn]; sid != "" {
				spec.SkillBindings = append(spec.SkillBindings, tasksvc.SkillBinding{SkillID: sid, SkillName: sn, Mode: "follow"})
			}
		}
		if _, err := a.Tasks.UpdateSpec(ctx, tv.Task.ID, spec, "pack", "imported from pack "+p.Name); err != nil {
			return nil, err
		}
		_ = a.Tasks.Pause(ctx, tv.Task.ID, tasksvc.PausePack, "")
		res.TaskIDs = append(res.TaskIDs, tv.Task.ID)
	}
	ids, _ := json.Marshal(res.TaskIDs)
	_ = a.Auth.SetSetting(ctx, "pack.imported."+res.Hash, string(ids))
	return res, nil
}
