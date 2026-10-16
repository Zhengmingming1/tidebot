package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// taskForm is the WebUI create/edit payload.
type taskForm struct {
	Name           string  `json:"name"`
	Prompt         string  `json:"prompt"`
	SkillID        string  `json:"skill_id"`
	SkillVersionID string  `json:"skill_version_id"`
	Provider       string  `json:"provider"`
	Cron           string  `json:"cron"`
	BudgetUSD      float64 `json:"budget_usd"`
	Enabled        bool    `json:"enabled"`
	TimeoutMinutes int     `json:"timeout_minutes"`
}

func (s *Server) validateTaskForm(ctx context.Context, f *taskForm) error {
	f.Name = strings.TrimSpace(f.Name)
	f.Prompt = strings.TrimSpace(f.Prompt)
	f.Cron = strings.TrimSpace(f.Cron)
	if f.Name == "" || utf8.RuneCountInString(f.Name) > 80 {
		return errors.New("name is required (max 80 characters)")
	}
	if f.Prompt == "" || len(f.Prompt) > 32<<10 {
		return errors.New("instruction is required (max 32 KB)")
	}
	if f.Provider != "" && s.deps.Reg != nil {
		if _, ok := s.deps.Reg.Get(f.Provider); !ok {
			return errors.New("unknown provider")
		}
	}
	if f.Cron != "" {
		if s.deps.Tasks.Sched == nil {
			return errors.New("scheduler unavailable")
		}
		if _, err := s.deps.Tasks.Sched.NextRun(f.Cron, time.Now()); err != nil {
			return errors.New("schedule: not a valid 5-field cron expression")
		}
	}
	if f.BudgetUSD < 0 || f.BudgetUSD > 10000 {
		return errors.New("budget must be between 0 and 10000 USD")
	}
	if f.TimeoutMinutes < 0 || f.TimeoutMinutes > 24*60 {
		return errors.New("timeout must be at most 1440 minutes")
	}
	if f.SkillID != "" {
		found := false
		list, _ := s.deps.Skills.List(ctx)
		for _, sk := range list {
			if sk.ID == f.SkillID {
				found = true
			}
		}
		if !found {
			return errors.New("unknown skill")
		}
		if f.SkillVersionID != "" {
			var n int
			_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM versions WHERE id=? AND entity_id=?`, f.SkillVersionID, f.SkillID).Scan(&n)
			if n == 0 {
				return errors.New("unknown skill version")
			}
		}
	} else {
		f.SkillVersionID = ""
	}
	return nil
}

func (s *Server) applyTaskExtras(ctx context.Context, taskID string, f taskForm) error {
	if err := s.deps.Tasks.SetSchedule(ctx, taskID, f.Cron); err != nil {
		return err
	}
	st := "paused"
	if f.Enabled {
		st = "active"
	}
	if err := s.deps.Tasks.SetStatus(ctx, taskID, st, "webui"); err != nil {
		return err
	}
	if s.deps.Cost != nil {
		return s.deps.Cost.UpsertBudget(ctx, cost.Budget{Scope: "task", ScopeID: taskID, Period: "day", Bucket: "",
			MaxUSD: sql.NullFloat64{Float64: f.BudgetUSD, Valid: f.BudgetUSD > 0}, Action: "pause_notify", WarnAtPct: 80})
	}
	return nil
}

func specFromForm(spec tasksvc.Spec, f taskForm) tasksvc.Spec {
	spec.Prompt = f.Prompt
	spec.Provider = f.Provider
	if f.TimeoutMinutes > 0 {
		spec.TimeoutMinutes = f.TimeoutMinutes
	}
	spec.SkillBindings = nil
	if f.SkillID != "" {
		b := tasksvc.SkillBinding{SkillID: f.SkillID, Mode: "follow"}
		if f.SkillVersionID != "" {
			b.Mode, b.VersionID = "pinned", f.SkillVersionID
		}
		spec.SkillBindings = []tasksvc.SkillBinding{b}
	}
	return spec
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var f taskForm
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&f); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ctx := r.Context()
	if err := s.validateTaskForm(ctx, &f); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tv, err := s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: f.Name, Prompt: f.Prompt, ProviderName: f.Provider,
		TimeoutMinutes: f.TimeoutMinutes, OverlapPolicy: "skip", Actor: "user"})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if f.SkillID != "" {
		if _, err := s.deps.Tasks.UpdateSpec(ctx, tv.Task.ID, specFromForm(*tv.Spec, f), "user", "bind skill"); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if err := s.applyTaskExtras(ctx, tv.Task.ID, f); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	v, _ := s.deps.Tasks.Get(ctx, tv.Task.ID)
	writeJSON(w, 201, toTaskDTO(v))
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request, id string) {
	var f taskForm
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&f); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ctx := r.Context()
	cur, err := s.deps.Tasks.Get(ctx, id)
	if err != nil || cur.Spec == nil {
		writeErr(w, 404, "task not found")
		return
	}
	if err := s.validateTaskForm(ctx, &f); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	next := specFromForm(*cur.Spec, f)
	a, _ := json.Marshal(cur.Spec)
	b, _ := json.Marshal(next)
	if string(a) != string(b) {
		if _, err := s.deps.Tasks.UpdateSpec(ctx, id, next, "user", "edited in WebUI"); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if f.Name != cur.Task.Name {
		if err := s.deps.Tasks.Rename(ctx, id, f.Name); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if err := s.applyTaskExtras(ctx, id, f); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	v, _ := s.deps.Tasks.Get(ctx, id)
	writeJSON(w, 200, toTaskDTO(v))
}

// taskFormOf returns the editable view of a task (for the edit dialog).
func (s *Server) taskFormOf(ctx context.Context, id string) (map[string]any, error) {
	v, err := s.deps.Tasks.Get(ctx, id)
	if err != nil || v.Spec == nil {
		return nil, errors.New("task not found")
	}
	f := taskForm{Name: v.Task.Name, Prompt: v.Spec.Prompt, Provider: v.Spec.Provider, Cron: v.Task.CronExpr,
		Enabled: v.Task.Status == "active", TimeoutMinutes: v.Spec.TimeoutMinutes}
	if len(v.Spec.SkillBindings) > 0 {
		f.SkillID = v.Spec.SkillBindings[0].SkillID
		if v.Spec.SkillBindings[0].Mode == "pinned" {
			f.SkillVersionID = v.Spec.SkillBindings[0].VersionID
		}
	}
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(max_usd,0) FROM budgets WHERE scope='task' AND scope_id=? AND period='day' AND bucket=''`, id).Scan(&f.BudgetUSD)
	return map[string]any{"form": f}, nil
}

// handleCronPreview: GET /api/scheduler/preview?cron=...&tz=...&n=5 → next run times.
// The scheduler always fires in the server timezone; tz only changes how the times are shown.
func (s *Server) handleCronPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	expr := strings.TrimSpace(q.Get("cron"))
	if len(expr) > 200 {
		writeErr(w, 400, "cron expression too long")
		return
	}
	show := s.loc()
	if tz := q.Get("tz"); tz != "" {
		l, err := time.LoadLocation(tz)
		if len(tz) > 64 || err != nil {
			writeErr(w, 400, "unknown timezone")
			return
		}
		show = l
	}
	n, _ := strconv.Atoi(q.Get("n"))
	if n <= 0 || n > 20 {
		n = 5
	}
	if expr == "" || s.deps.Tasks.Sched == nil {
		writeJSON(w, 200, map[string]any{"times": []string{}, "timezone": show.String(), "scheduler_timezone": s.loc().String()})
		return
	}
	times, err := PreviewRuns(s.deps.Tasks.Sched.NextRun, expr, time.Now(), n)
	if err != nil {
		writeErr(w, 400, "not a valid 5-field cron expression")
		return
	}
	out := make([]string, len(times))
	for i, t := range times {
		out[i] = t.In(show).Format(time.RFC3339)
	}
	writeJSON(w, 200, map[string]any{"times": out, "timezone": show.String(), "scheduler_timezone": s.loc().String()})
}

// PreviewRuns returns the next n fire times using the scheduler's own NextRun, so the
// preview cannot drift from what actually runs.
func PreviewRuns(next func(string, time.Time) (time.Time, error), expr string, from time.Time, n int) ([]time.Time, error) {
	out := make([]time.Time, 0, n)
	t := from
	for i := 0; i < n; i++ {
		nt, err := next(expr, t)
		if err != nil {
			return nil, err
		}
		out = append(out, nt)
		t = nt
	}
	return out, nil
}

// handleTaskBatch: POST /api/tasks/batch {ids, action: pause|resume|delete}.
func (s *Server) handleTaskBatch(w http.ResponseWriter, r *http.Request) {
	var b struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&b); err != nil || len(b.IDs) == 0 || len(b.IDs) > 200 {
		writeErr(w, 400, "ids (1-200) and action required")
		return
	}
	ctx := r.Context()
	done, failed := 0, []string{}
	for _, id := range b.IDs {
		v, err := s.deps.Tasks.Get(ctx, id)
		if err != nil || v.Task.Status == "archived" {
			failed = append(failed, id)
			continue
		}
		switch b.Action {
		case "pause":
			err = s.deps.Tasks.SetStatus(ctx, id, "paused", "webui batch")
		case "resume":
			err = s.deps.Tasks.SetStatus(ctx, id, "active", "webui batch")
		case "delete":
			if err = s.deps.Tasks.SetStatus(ctx, id, "archived", "deleted in WebUI (batch)"); err == nil {
				_ = s.deps.Tasks.SetSchedule(ctx, id, "")
			}
		default:
			writeErr(w, 400, "action must be pause, resume or delete")
			return
		}
		if err != nil {
			failed = append(failed, id)
			continue
		}
		done++
	}
	writeJSON(w, 200, map[string]any{"ok": len(failed) == 0, "done": done, "failed": failed})
}
