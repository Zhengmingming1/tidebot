package webui

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"net/url"

	"strconv"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/textclip"
)

// ---------- runs ----------

// runStatusGroups maps a runs-list filter to the statuses it covers. "failed" is every
// run that went wrong (a timeout is a failure too); "stopped" is a run that was ended or
// never started on purpose (stopped by hand, or skipped by budget / a paused task), which
// is not a fault. Any other value filters on that exact status, as before.
var runStatusGroups = map[string][]string{
	"running":   {"queued", "running"},
	"succeeded": {"succeeded"},
	"failed":    {"failed", "timeout"},
	"stopped":   {"canceled", "skipped"},
}

// runsFilter builds the WHERE part shared by the list and its counts: task, search text
// and (unless withStatus is false) the status filter. Everything goes in as a parameter.
func runsFilter(q url.Values, withStatus bool, loc *time.Location, now time.Time) (string, []any, error) {
	where := "WHERE 1=1"
	var args []any
	if from, to, ok, err := runsRange(q, loc, now); err != nil {
		return "", nil, err
	} else if ok {
		// created_at is RFC3339 in UTC, so the text compares in time order
		where += " AND r.created_at >= ? AND r.created_at < ?"
		args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	}
	if st := q.Get("status"); withStatus && st != "" {
		group, ok := runStatusGroups[st]
		if !ok {
			group = []string{st}
		}
		where += " AND r.status IN (?" + strings.Repeat(",?", len(group)-1) + ")"
		for _, g := range group {
			args = append(args, g)
		}
	}
	if tid := q.Get("task_id"); tid != "" {
		where += " AND r.task_id=?"
		args = append(args, tid)
	}
	if qs := strings.TrimSpace(q.Get("q")); qs != "" {
		if len(qs) > 100 {
			return "", nil, errors.New("search text too long")
		}
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(qs) + "%"
		where += ` AND (r.prompt LIKE ? ESCAPE '\' OR t.name LIKE ? ESCAPE '\' OR r.id LIKE ? ESCAPE '\' OR r.error_message LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like)
	}
	return where, args, nil
}

// runsRange turns the runs-list time filter into [from, to) instants. Days are calendar
// days in the scheduler's time zone (the zone every time on the page is shown in):
// range=today | yesterday | 7d | 30d (today and the days before it), or from=/to= as
// YYYY-MM-DD, both days included; either end may be left out. ok is false for no filter.
func runsRange(q url.Values, loc *time.Location, now time.Time) (from, to time.Time, ok bool, err error) {
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch rg := q.Get("range"); rg {
	case "", "all":
	case "today":
		return day0, day0.AddDate(0, 0, 1), true, nil
	case "yesterday":
		return day0.AddDate(0, 0, -1), day0, true, nil
	case "7d":
		return day0.AddDate(0, 0, -6), day0.AddDate(0, 0, 1), true, nil
	case "30d":
		return day0.AddDate(0, 0, -29), day0.AddDate(0, 0, 1), true, nil
	default:
		return from, to, false, errors.New("range must be today, yesterday, 7d or 30d")
	}
	fs, ts := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	if fs == "" && ts == "" {
		return from, to, false, nil
	}
	parse := func(name, v string) (time.Time, error) {
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return d, fmt.Errorf("%s must be a date like 2026-10-24", name)
		}
		return d, nil
	}
	from = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	to = day0.AddDate(0, 0, 1)
	if fs != "" {
		if from, err = parse("from", fs); err != nil {
			return
		}
	}
	if ts != "" {
		var d time.Time
		if d, err = parse("to", ts); err != nil {
			return
		}
		to = d.AddDate(0, 0, 1)
	}
	if !to.After(from) {
		return from, to, false, errors.New("from must not be after to")
	}
	return from, to, true, nil
}

// handleRuns lists runs, newest first by default. Paging: for the time sorts the next
// page starts after the last run shown (before=<run id>, a keyset cursor, so runs that
// start while you scroll don't shift the pages); the other sorts use offset.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where, args, err := runsFilter(q, true, s.loc(), time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// ids are time-ordered to the millisecond: they break ties between runs created in the
	// same second, so queued runs list in their real order.
	sortBy := q.Get("sort")
	order := s.runsOrder(sortBy)
	if order == "" {
		writeErr(w, 400, "sort must be new, old, cost, tokens or duration")
		return
	}
	if before := q.Get("before"); before != "" {
		if sortBy != "" && sortBy != "new" && sortBy != "old" {
			writeErr(w, 400, "before only works with sort=new or old; use offset")
			return
		}
		var created string
		if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT created_at FROM runs WHERE id=?`, before).Scan(&created); err != nil {
			writeErr(w, 400, "before: run not found")
			return
		}
		cmp := "<"
		if sortBy == "old" {
			cmp = ">"
		}
		where += " AND (r.created_at " + cmp + " ? OR (r.created_at = ? AND r.id " + cmp + " ?))"
		args = append(args, created, created, before)
	}
	page := " LIMIT ?"
	args = append(args, limitParam(r, 50, 1000))
	if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		page += " OFFSET ?"
		args = append(args, off)
	}
	// With search text, read whole prompts so a hit past the 300-byte preview still shows
	// up in the row's excerpt; the prompt is cut to the preview afterwards.
	qs := strings.TrimSpace(q.Get("q"))
	promptMax := listPromptMax
	if qs != "" {
		promptMax = 0
	}
	runs, err := s.queryRunsOpt(r.Context(), promptMax, runsPageWhere(qs != "", where, order, page), args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if qs != "" {
		for i := range runs {
			runs[i].Match = findRunHit(runs[i], qs)
			runs[i].Prompt = textclip.Ellipsis(runs[i].Prompt, listPromptMax)
		}
	}
	writeJSON(w, 200, runs)
}

// runsPageWhere picks the page's rows first and joins the rest onto them (idea: n8n
// execution.repository.ts toQueryBuilderWithAnnotations — the page is a subquery, the
// joins go on the page and it is sorted again). The inner query reads only runs and, for
// the time and status filters, only an index (idx_runs_created_id, idx_runs_status_created):
// the tasks join is there only when search text needs the task name. Before, SQLite joined
// tasks / versions / skills for every run and sorted them all with their prompts to keep 50
// (≈200 ms at 50 000 runs for the unfiltered list, ≈50 ms for "failed").
func runsPageWhere(search bool, where, order, limit string) string {
	join := ""
	if search {
		join = "LEFT JOIN tasks t ON t.id = r.task_id "
	}
	return "WHERE r.rowid IN (SELECT r.rowid FROM runs r " + join + where + " ORDER BY " + order + limit + ") ORDER BY " + order
}

// runsOrder is the ORDER BY for the runs list's sort (new, old, cost, tokens, duration);
// "" for an unknown one.
func (s *Server) runsOrder(sortBy string) string {
	return map[string]string{"": "r.created_at DESC, r.id DESC", "new": "r.created_at DESC, r.id DESC", "old": "r.created_at ASC, r.id ASC",
		"cost": s.runAmt("r.") + " DESC, r.created_at DESC, r.id DESC", "tokens": "(COALESCE(r.input_tokens,0)+COALESCE(r.output_tokens,0)) DESC, r.created_at DESC, r.id DESC",
		"duration": "COALESCE(r.duration_ms,0) DESC, r.created_at DESC, r.id DESC"}[sortBy]
}

// handleRunCounts: GET /api/runs/counts — how many runs each filter chip would show,
// for the same task and search text as the list. One grouped query.
func (s *Server) handleRunCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET only")
		return
	}
	where, args, err := runsFilter(r.URL.Query(), false, s.loc(), time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// tasks are only needed to match the search text against the task name; without it
	// the count is read from a status index alone (44 ms -> 2 ms at 50 000 runs).
	join := ""
	if strings.TrimSpace(r.URL.Query().Get("q")) != "" {
		join = "LEFT JOIN tasks t ON t.id = r.task_id "
	}
	rows, err := s.deps.DB.QueryContext(r.Context(), `SELECT r.status, COUNT(1) FROM runs r `+join+where+` GROUP BY r.status`, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := map[string]int{"all": 0}
	for g := range runStatusGroups {
		out[g] = 0
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out["all"] += n
		for g, list := range runStatusGroups {
			for _, x := range list {
				if x == st {
					out[g] += n
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) runStatus(ctx context.Context, id string) string {
	var st string
	if err := s.deps.DB.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, id).Scan(&st); err != nil {
		return ""
	}
	return st
}

func (s *Server) handleRunSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 404, "not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		runs, err := s.queryRunsFull(r.Context(), "WHERE r.id=?", id)
		if err != nil || len(runs) == 0 {
			writeErr(w, 404, "run not found")
			return
		}
		// cost_detail: the calculation (tokens × price snapshot) for the run detail page
		// result: the agent's final answer (Markdown, capped) — only on the detail, never in lists
		// answer_same: the earlier run of the task that gave the same answer (whitespace ignored)
		// file_changes: files an edit run changed in its folder (git work trees only)
		// changes_undone_at: when "Undo changes" took them back out of the folder ('' = in place)
		// changes_undone_files: files taken back one by one (while the run isn't wholly undone)
		var detail, result, sameID, changes, undoneAt, undoneFiles string
		var cut int
		_ = s.deps.DB.QueryRowContext(r.Context(), `SELECT cost_detail, result_text, result_truncated, answer_same_as, file_changes, changes_undone_at, changes_undone_files FROM runs WHERE id=?`, id).Scan(&detail, &result, &cut, &sameID, &changes, &undoneAt, &undoneFiles)
		out := struct {
			runDTO
			SkillBindings       []tasksvc.SkillBinding   `json:"skill_bindings"`
			SkillSetSource      *skillstore.RunSetSource `json:"skill_set_source,omitempty"`
			SkillEvidence       []skillEvidence          `json:"skill_evidence"`
			ContextPlan         json.RawMessage          `json:"context_plan,omitempty"`
			EffectiveCostPolicy json.RawMessage          `json:"effective_cost_policy,omitempty"`
			CostDetail          json.RawMessage          `json:"cost_detail"`
			Currency            string                   `json:"currency"`
			Result              string                   `json:"result"`
			ResultTruncated     bool                     `json:"result_truncated,omitempty"`
			AnswerSame          *answerSameDTO           `json:"answer_same,omitempty"`
			AnswerPrev          *answerSameDTO           `json:"answer_prev,omitempty"`
			FileChanges         json.RawMessage          `json:"file_changes,omitempty"`
			AgentConfigChanges  json.RawMessage          `json:"agent_config_changes,omitempty"`
			ChangesUndoneAt     string                   `json:"changes_undone_at,omitempty"`
			ChangesUndoneFiles  []string                 `json:"changes_undone_files,omitempty"`
			ChatSession         *runChatRef              `json:"chat_session,omitempty"`
		}{runDTO: runs[0], Currency: s.fx().Display, Result: result, ResultTruncated: cut != 0, ChangesUndoneAt: undoneAt}
		var policyJSON string
		if s.deps.DB.QueryRowContext(r.Context(), `SELECT effective_cost_policy_json FROM runs WHERE id=?`, id).Scan(&policyJSON) == nil && policyJSON != "{}" && json.Valid([]byte(policyJSON)) {
			out.EffectiveCostPolicy = json.RawMessage(policyJSON)
		}
		var contextAudit string
		if err := s.deps.DB.QueryRowContext(r.Context(), `SELECT detail_json FROM operation_log WHERE entity_type='run' AND entity_id=? AND action='context.plan' ORDER BY id DESC LIMIT 1`, id).Scan(&contextAudit); err == nil && json.Valid([]byte(contextAudit)) {
			out.ContextPlan = json.RawMessage(contextAudit)
		}
		out.SkillBindings, _ = s.deps.Skills.RunBindings(r.Context(), id)
		out.SkillSetSource, _ = s.deps.Skills.RunSource(r.Context(), id)
		out.SkillEvidence = loadedSkillEvidence(out.SkillBindings)
		if len(out.SkillBindings) == 0 && runs[0].SkillID != "" {
			mode := "follow"
			if runs[0].SkillVerID != "" {
				mode = "pinned"
			}
			out.SkillBindings = []tasksvc.SkillBinding{{SkillID: runs[0].SkillID, SkillName: runs[0].SkillName, VersionID: runs[0].SkillVerID, Mode: mode, Role: "primary"}}
		}
		if undoneAt == "" && undoneFiles != "" {
			_ = json.Unmarshal([]byte(undoneFiles), &out.ChangesUndoneFiles)
		}
		if changes != "" && json.Valid([]byte(changes)) {
			out.FileChanges = json.RawMessage(changes)
		}
		// agent_config_changes: agent config a command run's after-run check found changed (not undone)
		var acc string
		if s.deps.DB.QueryRowContext(r.Context(), `SELECT agent_config_changes FROM runs WHERE id=?`, id).Scan(&acc) == nil && acc != "" && json.Valid([]byte(acc)) {
			out.AgentConfigChanges = json.RawMessage(acc)
		}
		if sameID != "" {
			var at string
			if s.deps.DB.QueryRowContext(r.Context(), `SELECT COALESCE(finished_at, created_at) FROM runs WHERE id=?`, sameID).Scan(&at) == nil {
				out.AnswerSame = &answerSameDTO{RunID: sameID, At: at}
			}
		}
		// answer_prev: the answer this one is compared with on the page ("compare with last time")
		if result != "" {
			if pid, _, at, err := s.prevAnswer(r.Context(), id); err == nil && pid != "" {
				out.AnswerPrev = &answerSameDTO{RunID: pid, At: at}
			}
		}
		if json.Valid([]byte(detail)) {
			out.CostDetail = json.RawMessage(detail)
		}
		out.ChatSession = s.runChatRef(r.Context(), id)
		writeJSON(w, 200, out)
		return
	}
	if parts[1] == "rerun" && r.Method == http.MethodPost {
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.rerun(w, r, id) })).ServeHTTP(w, r)
		return
	}
	if parts[1] == "stop" && r.Method == http.MethodPost {
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.stopRun(w, r, id) })).ServeHTTP(w, r)
		return
	}
	if parts[1] == "undo-changes" && r.Method == http.MethodPost {
		auth.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.undoRunChanges(w, r, id) })).ServeHTTP(w, r)
		return
	}
	if parts[1] == "undo-check" && r.Method == http.MethodGet {
		s.undoCheck(w, r, id)
		return
	}
	if parts[1] == "changes" && r.Method == http.MethodGet {
		s.handleRunChanges(w, r, id)
		return
	}
	if parts[1] == "answer-diff" && r.Method == http.MethodGet {
		s.answerDiff(w, r, id)
		return
	}
	if parts[1] != "events" {
		writeErr(w, 404, "not found")
		return
	}
	s.streamRunLog(w, r, id)
}

func isTerminal(st string) bool {
	switch st {
	case "succeeded", "failed", "canceled", "timeout", "skipped":
		return true
	}
	return false
}
