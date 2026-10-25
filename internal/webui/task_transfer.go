package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// Task export / import: move tasks to another machine (or keep them as a backup) as one
// JSON file. The file holds what a task *does* — name, instruction, requirements,
// schedule, model access by name, skill by id and name, limits and run policy — and never
// what belongs to this machine or person: no working folder (a local path, and paths
// differ between macOS / Windows / Linux), no run history or logs, no trigger / webhook
// tokens, no notification channels, no API keys. Imported tasks arrive paused unless the
// user asks otherwise, and a preview lists what will change before anything is written.

const (
	taskFileKind    = "tidebot.tasks"
	taskFileVersion = 1
	maxImportTasks  = 200
	maxImportBytes  = 4 << 20
)

type taskFile struct {
	Kind       string          `json:"kind"`
	Version    int             `json:"version"`
	ExportedAt string          `json:"exported_at,omitempty"`
	App        string          `json:"app,omitempty"`
	Tasks      []taskFileEntry `json:"tasks"`
}

type taskFileSkill struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type taskFileEntry struct {
	Name           string          `json:"name"`
	Goal           string          `json:"goal,omitempty"`
	Prompt         string          `json:"prompt"`
	Constraints    []string        `json:"constraints,omitempty"`
	Cron           string          `json:"cron,omitempty"`
	Enabled        bool            `json:"enabled"`
	TimeoutMinutes int             `json:"timeout_minutes,omitempty"`
	BudgetUSD      float64         `json:"budget_usd,omitempty"`    // daily cap of the task
	MaxRunUSD      float64         `json:"max_run_usd,omitempty"`   // per-run cap in the spec
	Provider       string          `json:"provider,omitempty"`      // name only, never a key
	Skill          *taskFileSkill  `json:"skill,omitempty"`         // follows the skill's current version
	AllowedTools   []string        `json:"allowed_tools,omitempty"` // as the task has them
	Policy         *taskPolicyForm `json:"policy,omitempty"`        // retries, catch-up, priority
}

// exportTasks: GET /api/tasks/export[?ids=a,b] → tidebot-tasks-YYYYMMDD.json (all live
// tasks when ids is empty).
func (s *Server) exportTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	want := map[string]bool{}
	for _, x := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			want[x] = true
		}
	}
	if len(want) > maxImportTasks {
		writeErr(w, 400, "too many tasks (max 200)")
		return
	}
	list, err := s.deps.Tasks.List(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	f := taskFile{Kind: taskFileKind, Version: taskFileVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339), App: "tidebot " + buildinfo.Version, Tasks: []taskFileEntry{}}
	for _, v := range list {
		if v.Task.Status == "archived" || v.Spec == nil || (len(want) > 0 && !want[v.Task.ID]) {
			continue
		}
		f.Tasks = append(f.Tasks, s.taskEntryOf(ctx, v))
	}
	if len(want) > 0 && len(f.Tasks) == 0 {
		writeErr(w, 404, "task not found")
		return
	}
	bs, _ := json.MarshalIndent(f, "", "  ")
	file := "tidebot-tasks-" + time.Now().In(s.loc()).Format("20060102") + ".json"
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+file+`"; filename*=UTF-8''`+url.PathEscape(file))
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(append(bs, '\n'))
}

func (s *Server) taskEntryOf(ctx context.Context, v tasksvc.TaskView) taskFileEntry {
	sp := v.Spec
	e := taskFileEntry{Name: v.Task.Name, Prompt: sp.Prompt, Cron: v.Task.CronExpr, Enabled: v.Task.Status == "active",
		TimeoutMinutes: sp.TimeoutMinutes, MaxRunUSD: sp.MaxBudgetUSD, Provider: sp.Provider, AllowedTools: sp.AllowedTools}
	if sp.Goal != sp.Prompt {
		e.Goal = sp.Goal
	}
	for _, c := range sp.Constraints {
		e.Constraints = append(e.Constraints, c.Text)
	}
	if len(sp.SkillBindings) > 0 {
		b := sp.SkillBindings[0]
		e.Skill = &taskFileSkill{ID: b.SkillID, Name: b.SkillName}
		if e.Skill.Name == "" && s.deps.Skills != nil {
			if all, err := s.deps.Skills.List(ctx); err == nil {
				for _, k := range all {
					if k.ID == b.SkillID {
						e.Skill.Name = k.Name
					}
				}
			}
		}
	}
	if s.deps.DB != nil {
		_ = s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(max_usd,0) FROM budgets WHERE scope='task' AND scope_id=? AND period='day' AND bucket=''`, v.Task.ID).Scan(&e.BudgetUSD)
		pf := policyFormOf((&policy.Store{DB: s.deps.DB}).Get(ctx, v.Task.ID))
		e.Policy = &pf
	}
	return e
}

// importResult is one row of the preview / outcome.
type importResult struct {
	Index  int      `json:"index"`
	Name   string   `json:"name"`           // the name it gets here (may be numbered)
	From   string   `json:"from,omitempty"` // the name in the file when it was renamed
	Cron   string   `json:"cron,omitempty"`
	Prompt string   `json:"prompt,omitempty"` // first 200 characters, for the preview
	Notes  []string `json:"notes,omitempty"`  // renamed | skill_missing | provider_missing | budget_dropped
	Error  string   `json:"error,omitempty"`  // set → not importable
	TaskID string   `json:"task_id,omitempty"`
}

// importTasks: POST /api/tasks/import {file, apply, pick: [index…], enable}.
// apply=false only checks (nothing is written); apply=true creates the picked rows
// (all valid rows when pick is absent). enable=false (default) imports them paused.
func (s *Server) importTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File   json.RawMessage `json:"file"`
		Apply  bool            `json:"apply"`
		Pick   []int           `json:"pick"`
		Enable bool            `json:"enable"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxImportBytes))
	if err := dec.Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	f, code, err := parseTaskFile(body.File)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	ctx := r.Context()
	taken, err := s.liveTaskNames(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	pick := map[int]bool{}
	for _, i := range body.Pick {
		pick[i] = true
	}
	out := make([]importResult, 0, len(f.Tasks))
	created := 0
	for i := range f.Tasks {
		e := f.Tasks[i]
		res, form, spec := s.checkImport(ctx, i, &e, taken)
		if res.Error == "" && body.Apply && (body.Pick == nil || pick[i]) {
			form.Enabled = body.Enable && e.Enabled
			tid, err := s.createImported(ctx, form, spec)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.TaskID = tid
				created++
			}
		}
		if res.Error == "" {
			taken[res.Name] = true
		}
		out = append(out, res)
	}
	writeJSON(w, 200, map[string]any{"tasks": out, "created": created, "exported_at": f.ExportedAt, "app": f.App})
}

func parseTaskFile(raw json.RawMessage) (taskFile, int, error) {
	var f taskFile
	if len(raw) == 0 {
		return f, 400, errors.New("file required")
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, 400, errors.New("not a tidebot task file")
	}
	if f.Kind != taskFileKind {
		return f, 400, errors.New("not a tidebot task file")
	}
	if f.Version > taskFileVersion {
		return f, 400, errors.New("made by a newer tidebot; update first")
	}
	if len(f.Tasks) == 0 {
		return f, 400, errors.New("no tasks in the file")
	}
	if len(f.Tasks) > maxImportTasks {
		return f, 400, errors.New("too many tasks (max 200)")
	}
	return f, 0, nil
}

func (s *Server) liveTaskNames(ctx context.Context) (map[string]bool, error) {
	list, err := s.deps.Tasks.List(ctx)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, v := range list {
		if v.Task.Status != "archived" {
			taken[v.Task.Name] = true
		}
	}
	return taken, nil
}

// numberedName: base, else "base 2", "base 3"… (as Duplicate does), within 80 characters.
func numberedName(base string, taken map[string]bool) string {
	for i := 1; i < 1000; i++ {
		suffix := ""
		if i > 1 {
			suffix = " " + strconv.Itoa(i)
		}
		r := []rune(base)
		if keep := 80 - utf8.RuneCountInString(suffix); len(r) > keep {
			r = r[:keep]
		}
		if n := strings.TrimSpace(string(r)) + suffix; !taken[n] {
			return n
		}
	}
	return ""
}

// checkImport validates one entry and maps it onto this machine: a missing model access
// falls back to the default one, a missing skill is left out, a taken name is numbered.
func (s *Server) checkImport(ctx context.Context, i int, e *taskFileEntry, taken map[string]bool) (importResult, taskForm, tasksvc.Spec) {
	e.Name = strings.TrimSpace(e.Name)
	res := importResult{Index: i, Name: e.Name, Cron: strings.TrimSpace(e.Cron)}
	if r := []rune(strings.TrimSpace(e.Prompt)); len(r) > 200 {
		res.Prompt = string(r[:200])
	} else {
		res.Prompt = string(r)
	}
	if e.Name == "" {
		e.Name = firstLine(e.Prompt, 40)
		res.Name = e.Name
	}
	if e.Policy != nil && e.Policy.Catchup == "" { // a hand-written file may give only some fields
		d := policyFormOf(policy.Default(""))
		e.Policy.Catchup, e.Policy.CatchupGraceMinutes = d.Catchup, d.CatchupGraceMinutes
	}
	f := taskForm{Name: e.Name, Prompt: e.Prompt, Provider: strings.TrimSpace(e.Provider), Cron: e.Cron,
		BudgetUSD: e.BudgetUSD, TimeoutMinutes: e.TimeoutMinutes, Policy: e.Policy}
	if f.Provider != "" && s.deps.Reg != nil {
		if _, ok := s.deps.Reg.Get(f.Provider); !ok {
			f.Provider = ""
			res.Notes = append(res.Notes, "provider_missing")
		}
	}
	if e.Skill != nil && s.deps.Skills != nil {
		if all, err := s.deps.Skills.List(ctx); err == nil {
			for _, k := range all {
				if k.ID == e.Skill.ID {
					f.SkillID = k.ID
				}
			}
			for _, k := range all {
				if f.SkillID == "" && e.Skill.Name != "" && k.Name == e.Skill.Name {
					f.SkillID = k.ID
				}
			}
		}
		if f.SkillID == "" {
			res.Notes = append(res.Notes, "skill_missing")
		}
	}
	if err := s.validateTaskForm(ctx, &f, ""); err != nil {
		res.Error = err.Error()
		return res, f, tasksvc.Spec{}
	}
	if e.MaxRunUSD < 0 || e.MaxRunUSD > 10000 || len(e.Constraints) > 100 || len(e.AllowedTools) > 100 || len(e.Goal) > 32<<10 {
		res.Error = "limits out of range"
		return res, f, tasksvc.Spec{}
	}
	sp := tasksvc.Spec{Goal: strings.TrimSpace(e.Goal), MaxBudgetUSD: e.MaxRunUSD}
	for _, c := range e.Constraints {
		if c = strings.TrimSpace(c); c != "" && len(c) <= 4<<10 {
			sp.Constraints = append(sp.Constraints, tasksvc.SpecConstraint{ID: id.New("c"), Text: c, Source: "import"})
		}
	}
	for _, t := range e.AllowedTools {
		if t = strings.TrimSpace(t); t != "" && len(t) <= 200 {
			sp.AllowedTools = append(sp.AllowedTools, t)
		}
	}
	if n := numberedName(f.Name, taken); n != f.Name {
		if n == "" {
			res.Error = "no free name"
			return res, f, sp
		}
		res.From, res.Name, f.Name = f.Name, n, n
		res.Notes = append(res.Notes, "renamed")
	}
	return res, f, sp
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max]))
	}
	return s
}

// createImported writes one checked entry: the task with its spec as version 1's
// successor (so the history says "imported"), then schedule, status, policy and budget.
func (s *Server) createImported(ctx context.Context, f taskForm, extra tasksvc.Spec) (string, error) {
	goal := extra.Goal
	tv, err := s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: f.Name, Prompt: f.Prompt, Goal: goal, ProviderName: f.Provider,
		TimeoutMinutes: f.TimeoutMinutes, OverlapPolicy: "skip", Actor: "user"})
	if err != nil {
		return "", err
	}
	undo := func(err error) (string, error) { // no half-made task left behind
		_ = s.deps.Tasks.SetStatus(ctx, tv.Task.ID, "archived", "import failed")
		_ = s.deps.Tasks.SetSchedule(ctx, tv.Task.ID, "")
		return "", err
	}
	sp := specFromForm(*tv.Spec, f)
	sp.Constraints, sp.AllowedTools, sp.MaxBudgetUSD = extra.Constraints, extra.AllowedTools, extra.MaxBudgetUSD
	if len(sp.Constraints) > 0 || len(sp.AllowedTools) > 0 || sp.MaxBudgetUSD > 0 || len(sp.SkillBindings) > 0 {
		if _, err := s.deps.Tasks.UpdateSpec(ctx, tv.Task.ID, sp, "user", "imported"); err != nil {
			return undo(err)
		}
	}
	if err := s.applyTaskExtras(ctx, tv.Task.ID, f); err != nil {
		return undo(err)
	}
	return tv.Task.ID, nil
}
