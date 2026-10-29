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
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
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
	// Workspace: the task's own working folder, "" = the per-task default. nil (field
	// left out, e.g. an older page) keeps the folder the task has.
	Workspace *string `json:"workspace,omitempty"`
	// Policy: retries, catch-up and success notices. nil (left out) keeps what the task has.
	Policy *taskPolicyForm `json:"policy,omitempty"`
	// Tags: nil (left out, e.g. an older page or agentctl) keeps the task's tags.
	Tags *[]string `json:"tags,omitempty"`
}

// taskPolicyForm is the part of policy.Policy the task form edits. Allowed tools and
// side effects are left as they are; Priority "" (an older page) keeps the task's.
type taskPolicyForm struct {
	RetryMax            int    `json:"retry_max"`
	Catchup             string `json:"catchup"`
	CatchupGraceMinutes int    `json:"catchup_grace_minutes"`
	NotifySuccess       bool   `json:"notify_success"`
	NotifyAnswer        *bool  `json:"notify_answer,omitempty"`  // nil (an older page or file) keeps the task's
	NotifyChanged       *bool  `json:"notify_changed,omitempty"` // only when the answer changed; nil keeps the task's
	Priority            string `json:"priority,omitempty"`       // urgent | normal | background (power saving)
}

// maxCatchupGrace: missed runs older than a week are not worth running any more.
const maxCatchupGrace = 7 * 24 * 60

func validatePolicyForm(p *taskPolicyForm) error {
	if p.RetryMax < 0 || p.RetryMax > 5 {
		return errors.New("retries must be 0-5")
	}
	switch p.Catchup {
	case "skip", "run_once", "run_all":
	default:
		return errors.New("unknown catch-up mode")
	}
	if p.CatchupGraceMinutes < 1 || p.CatchupGraceMinutes > maxCatchupGrace {
		return errors.New("catch-up window must be 1 minute to 7 days")
	}
	switch p.Priority {
	case "", "urgent", "normal", "background":
	default:
		return errors.New("priority must be urgent, normal or background")
	}
	return nil
}

// policyFormOf is the editable policy of a task (defaults when it has none yet).
func policyFormOf(p policy.Policy) taskPolicyForm {
	na, nc := p.NotifyAnswer, p.NotifyChanged
	return taskPolicyForm{RetryMax: p.RetryMax, Catchup: p.Catchup, CatchupGraceMinutes: p.CatchupGraceMinutes, NotifySuccess: p.NotifySuccess, NotifyAnswer: &na, NotifyChanged: &nc, Priority: p.Priority}
}

func (s *Server) validateTaskForm(ctx context.Context, f *taskForm, taskID string) error {
	if f.Workspace != nil {
		ws, err := s.normalizeWorkspace(*f.Workspace, taskID)
		if err != nil {
			return err
		}
		f.Workspace = &ws
	}
	if f.Policy != nil {
		if err := validatePolicyForm(f.Policy); err != nil {
			return err
		}
	}
	if f.Tags != nil {
		tags, err := normalizeTags(*f.Tags)
		if err != nil {
			return err
		}
		tags = s.canonicalTags(ctx, tags)
		f.Tags = &tags
	}
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
	if f.Policy != nil && s.deps.DB != nil {
		ps := &policy.Store{DB: s.deps.DB}
		p := ps.Get(ctx, taskID) // keeps tools, side effects and the failure count
		p.RetryMax, p.Catchup, p.CatchupGraceMinutes, p.NotifySuccess = f.Policy.RetryMax, f.Policy.Catchup, f.Policy.CatchupGraceMinutes, f.Policy.NotifySuccess
		if f.Policy.Priority != "" {
			p.Priority = f.Policy.Priority
		}
		if f.Policy.NotifyAnswer != nil {
			p.NotifyAnswer = *f.Policy.NotifyAnswer
		}
		if f.Policy.NotifyChanged != nil {
			p.NotifyChanged = *f.Policy.NotifyChanged
		}
		if err := ps.Put(ctx, p); err != nil {
			return err
		}
	}
	if f.Tags != nil && s.deps.DB != nil {
		if err := s.setTaskTags(ctx, taskID, *f.Tags); err != nil {
			return err
		}
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
	if f.Workspace != nil {
		spec.WorkspaceDir = *f.Workspace
	}
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
	if err := s.validateTaskForm(ctx, &f, ""); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ws := ""
	if f.Workspace != nil {
		ws = *f.Workspace
	}
	tv, err := s.deps.Tasks.Create(ctx, tasksvc.CreateInput{Name: f.Name, Prompt: f.Prompt, ProviderName: f.Provider, WorkspaceDir: ws,
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
	if err := s.validateTaskForm(ctx, &f, id); err != nil {
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
	ws := v.Spec.WorkspaceDir
	if ws == "" {
		ws = v.Task.WorkspaceDir
	}
	if _, def := s.taskWorkspace(id, ws); def {
		ws = ""
	}
	f.Workspace = &ws
	if len(v.Spec.SkillBindings) > 0 {
		f.SkillID = v.Spec.SkillBindings[0].SkillID
		if v.Spec.SkillBindings[0].Mode == "pinned" {
			f.SkillVersionID = v.Spec.SkillBindings[0].VersionID
		}
	}
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COALESCE(max_usd,0) FROM budgets WHERE scope='task' AND scope_id=? AND period='day' AND bucket=''`, id).Scan(&f.BudgetUSD)
	p := (&policy.Store{DB: s.deps.DB}).Get(ctx, id)
	pf := policyFormOf(p)
	f.Policy = &pf
	tags := []string{}
	if m, err := s.taskTags(ctx, id); err == nil && m[id] != nil {
		tags = m[id]
	}
	f.Tags = &tags
	return map[string]any{"form": f, "fail_streak": p.ConsecutiveFailures, "breaker_at": policy.BreakerThreshold}, nil
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
	res := map[string]any{"times": out, "timezone": show.String(), "scheduler_timezone": s.loc().String()}
	// A fixed daily time that a clock change moves or merges within the next year.
	if sch, err := cronParser.Parse(expr); err == nil {
		if n := scheduler.NextDSTNote(sch, time.Now(), s.loc(), 366*24*time.Hour); n != nil {
			res["dst"] = n
		}
	}
	writeJSON(w, 200, res)
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
		Tag    string   `json:"tag"` // add_tag / remove_tag
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&b); err != nil || len(b.IDs) == 0 || len(b.IDs) > 200 {
		writeErr(w, 400, "ids (1-200) and action required")
		return
	}
	ctx := r.Context()
	if b.Action == "add_tag" || b.Action == "remove_tag" {
		t, err := s.batchTagArg(ctx, b.Tag)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		b.Tag = t
	}
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
		case "add_tag", "remove_tag":
			err = s.batchTag(ctx, id, b.Action, b.Tag)
		default:
			writeErr(w, 400, "action must be pause, resume, delete, add_tag or remove_tag")
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

// duplicateTask: POST /api/tasks/{id}/duplicate {name?} → the new task (paused).
// Copies the current spec (prompt, constraints, skill binding, tools, budget cap),
// schedule, task budgets and run policy. Not copied: history, runs, triggers/webhooks
// (their tokens must stay unique) and per-task notification rules.
func (s *Server) duplicateTask(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
	}
	ctx := r.Context()
	src, err := s.deps.Tasks.Get(ctx, id)
	if err != nil || src.Spec == nil || src.Task.Status == "archived" {
		writeErr(w, 404, "task not found")
		return
	}
	base := strings.TrimSpace(body.Name)
	if base == "" {
		base = src.Task.Name + " (copy)"
	}
	name, err := s.deps.Tasks.UniqueName(ctx, base, 80)
	if err != nil || name == "" {
		writeErr(w, 400, "name is required (max 80 characters)")
		return
	}
	tv, err := s.deps.Tasks.Duplicate(ctx, id, name, "user")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := s.copyTaskSettings(ctx, id, tv.Task.ID); err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("duplicate: budgets/policy not copied", "task", tv.Task.ID, "err", err)
		}
	}
	v, _ := s.deps.Tasks.Get(ctx, tv.Task.ID)
	writeJSON(w, 201, toTaskDTO(v))
}

func (s *Server) copyTaskSettings(ctx context.Context, from, to string) error {
	if m, err := s.taskTags(ctx, from); err != nil {
		return err
	} else if len(m[from]) > 0 {
		if err := s.setTaskTags(ctx, to, m[from]); err != nil {
			return err
		}
	}
	if s.deps.Cost != nil {
		rows, err := s.deps.DB.QueryContext(ctx, `SELECT period, max_usd, max_tokens, action, warn_at_pct, bucket, currency
FROM budgets WHERE scope='task' AND scope_id=?`, from)
		if err != nil {
			return err
		}
		var list []cost.Budget
		for rows.Next() {
			b := cost.Budget{Scope: "task", ScopeID: to}
			if err := rows.Scan(&b.Period, &b.MaxUSD, &b.MaxTokens, &b.Action, &b.WarnAtPct, &b.Bucket, &b.Currency); err != nil {
				rows.Close()
				return err
			}
			list = append(list, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, b := range list {
			if err := s.deps.Cost.UpsertBudget(ctx, b); err != nil {
				return err
			}
		}
	}
	var n int
	_ = s.deps.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM task_policies WHERE task_id=?`, from).Scan(&n)
	if n > 0 {
		ps := &policy.Store{DB: s.deps.DB}
		p := ps.Get(ctx, from)
		p.TaskID, p.ConsecutiveFailures = to, 0
		return ps.Put(ctx, p)
	}
	return nil
}
