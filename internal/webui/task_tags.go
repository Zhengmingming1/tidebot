package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Task tags group tasks in the list ("日报", "监控", "客户 A"). Like Uptime Kuma's monitor
// tags (src/components/TagsManager.vue: names compared without case, picked from the ones
// already in use; MonitorList.vue: the search also matches tag names, a tag filter), but
// plain words: no colours to choose (the WebUI derives a fixed one from the name) and no
// values. Stored in task_tags; archived tasks keep theirs but are never listed.
const (
	maxTaskTags  = 8
	maxTagRunes  = 24
	maxTagsInUse = 200
)

var errBadTag = errors.New("tags: up to 8, each 1-24 characters, no commas")

// normalizeTags trims, folds inner whitespace, drops a leading "#", removes duplicates
// (case-insensitive, the first spelling wins) and keeps the given order.
func normalizeTags(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.Join(strings.Fields(strings.TrimLeft(strings.TrimSpace(t), "#＃")), " ")
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagRunes || strings.ContainsAny(t, ",，") {
			return nil, errBadTag
		}
		for _, r := range t {
			if unicode.IsControl(r) {
				return nil, errBadTag
			}
		}
		k := strings.ToLower(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	if len(out) > maxTaskTags {
		return nil, errBadTag
	}
	return out, nil
}

// canonicalTags maps each tag onto the spelling already in use on another task, so
// "Daily" typed on one task joins the existing "daily" group instead of starting a second.
func (s *Server) canonicalTags(ctx context.Context, tags []string) []string {
	if len(tags) == 0 || s.deps.DB == nil {
		return tags
	}
	inUse, err := s.tagsInUse(ctx)
	if err != nil {
		return tags
	}
	by := make(map[string]string, len(inUse))
	for _, u := range inUse {
		by[strings.ToLower(u.Tag)] = u.Tag
	}
	out := make([]string, len(tags))
	for i, t := range tags {
		if c, ok := by[strings.ToLower(t)]; ok {
			out[i] = c
		} else {
			out[i] = t
		}
	}
	return out
}

func (s *Server) setTaskTags(ctx context.Context, taskID string, tags []string) error {
	tx, err := s.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_tags WHERE task_id=?`, taskID); err != nil {
		return err
	}
	for i, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_tags(task_id, tag, pos) VALUES (?,?,?)`, taskID, t, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// taskTags returns the tags of the given tasks (all live tasks when ids is empty).
func (s *Server) taskTags(ctx context.Context, ids ...string) (map[string][]string, error) {
	out := map[string][]string{}
	if s.deps.DB == nil {
		return out, nil
	}
	var rows *sql.Rows
	var err error
	if len(ids) == 1 {
		rows, err = s.deps.DB.QueryContext(ctx, `SELECT task_id, tag FROM task_tags WHERE task_id=? ORDER BY pos, tag`, ids[0])
	} else {
		rows, err = s.deps.DB.QueryContext(ctx, `SELECT g.task_id, g.tag FROM task_tags g JOIN tasks t ON t.id = g.task_id
WHERE t.status <> 'archived' ORDER BY g.task_id, g.pos, g.tag`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, tag string
		if err := rows.Scan(&tid, &tag); err != nil {
			return nil, err
		}
		out[tid] = append(out[tid], tag)
	}
	return out, rows.Err()
}

type tagUse struct {
	Tag string `json:"tag"`
	N   int    `json:"n"`
}

// tagsInUse: every tag on a live task with how many tasks carry it, most used first.
func (s *Server) tagsInUse(ctx context.Context) ([]tagUse, error) {
	rows, err := s.deps.DB.QueryContext(ctx, `SELECT g.tag, COUNT(1) FROM task_tags g JOIN tasks t ON t.id = g.task_id
WHERE t.status <> 'archived' GROUP BY g.tag ORDER BY COUNT(1) DESC, g.tag LIMIT ?`, maxTagsInUse)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tagUse{}
	for rows.Next() {
		var u tagUse
		if err := rows.Scan(&u.Tag, &u.N); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// handleTagsInUse: GET /api/tasks/tags → [{tag, n}] for the form's suggestions.
func (s *Server) handleTagsInUse(w http.ResponseWriter, r *http.Request) {
	list, err := s.tagsInUse(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

// batchTag adds or removes one tag on many tasks (task list batch bar). Adding keeps the
// task's other tags and refuses a task that already has the maximum.
func (s *Server) batchTag(ctx context.Context, taskID, action, tag string) error {
	cur, err := s.taskTags(ctx, taskID)
	if err != nil {
		return err
	}
	have := cur[taskID]
	next := []string{}
	for _, t := range have {
		if !strings.EqualFold(t, tag) {
			next = append(next, t)
		}
	}
	if action == "add_tag" {
		next = append(next, tag)
		if len(next) > maxTaskTags {
			return errBadTag
		}
	}
	return s.setTaskTags(ctx, taskID, next)
}

// batchTagArg checks the tag of an add_tag / remove_tag batch.
func (s *Server) batchTagArg(ctx context.Context, raw string) (string, error) {
	t, err := normalizeTags([]string{raw})
	if err != nil || len(t) != 1 {
		return "", errBadTag
	}
	return s.canonicalTags(ctx, t)[0], nil
}

// ---- Settings › Tags: rename, merge or delete a tag everywhere ----
// Like Uptime Kuma's tag settings (src/components/settings/Tags.vue: every tag with how many
// monitors carry it, edit or delete from one list; TagEditDialog.vue checks the new name
// against the existing ones). Uptime Kuma refuses a name already in use; here that name
// merges the two tags instead (Linear's labels do the same), since our tags are just words.
// Every change answers with the affected tasks' tags from before, which /restore puts back
// exactly (the toast's undo). Archived tasks change too, so a restored task matches.

const maxTagRestore = 5000

type taskTagsBefore struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
}

type tagEditResult struct {
	Tag    string           `json:"tag,omitempty"` // the name it has now (rename)
	Tasks  int              `json:"tasks"`         // live tasks that carried it
	Merged bool             `json:"merged"`        // renamed onto a tag already in use
	Before []taskTagsBefore `json:"before"`        // for undo
}

var errTagNotInUse = errors.New("tag not in use")

func writeTaskTags(ctx context.Context, tx *sql.Tx, taskID string, tags []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_tags WHERE task_id=?`, taskID); err != nil {
		return err
	}
	for i, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_tags(task_id, tag, pos) VALUES (?,?,?)`, taskID, t, i); err != nil {
			return err
		}
	}
	return nil
}

// editTagEverywhere renames from → to on every task (to == "" deletes it). Names compare
// without case (Unicode-aware, in Go: SQLite's lower() only folds ASCII). A task that already
// has the new name keeps one copy, in the earlier position.
func (s *Server) editTagEverywhere(ctx context.Context, from, to string) (tagEditResult, error) {
	res := tagEditResult{Before: []taskTagsBefore{}}
	tx, err := s.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT g.task_id, g.tag, t.status <> 'archived' FROM task_tags g JOIN tasks t ON t.id = g.task_id
ORDER BY g.task_id, g.pos, g.tag`)
	if err != nil {
		return res, err
	}
	type taskRow struct {
		tags []string
		live bool
	}
	all := map[string]*taskRow{}
	order := []string{}
	for rows.Next() {
		var id, tag string
		var live bool
		if err := rows.Scan(&id, &tag, &live); err != nil {
			rows.Close()
			return res, err
		}
		r := all[id]
		if r == nil {
			r = &taskRow{live: live}
			all[id] = r
			order = append(order, id)
		}
		r.tags = append(r.tags, tag)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	// Merging onto a tag in use takes its spelling ("daily" → the existing "Daily").
	if to != "" && !strings.EqualFold(from, to) {
		for _, id := range order {
			if !all[id].live {
				continue
			}
			for _, t := range all[id].tags {
				if strings.EqualFold(t, to) {
					to, res.Merged = t, true
					break
				}
			}
			if res.Merged {
				break
			}
		}
	}
	for _, id := range order {
		r := all[id]
		hit := false
		for _, t := range r.tags {
			if strings.EqualFold(t, from) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		next := []string{}
		for _, t := range r.tags {
			if strings.EqualFold(t, from) {
				if to == "" {
					continue
				}
				t = to
			}
			dup := false
			for _, n := range next {
				if strings.EqualFold(n, t) {
					dup = true
					break
				}
			}
			if !dup {
				next = append(next, t)
			}
		}
		if err := writeTaskTags(ctx, tx, id, next); err != nil {
			return res, err
		}
		res.Before = append(res.Before, taskTagsBefore{ID: id, Tags: r.tags})
		if r.live {
			res.Tasks++
		}
	}
	if res.Tasks == 0 {
		return res, errTagNotInUse
	}
	res.Tag = to
	return res, tx.Commit()
}

// restoreTaskTags puts back the tags a rename / delete answered with. Unknown tasks are
// skipped; each list must still be a valid set of tags.
func (s *Server) restoreTaskTags(ctx context.Context, items []taskTagsBefore) (int, error) {
	tx, err := s.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, it := range items {
		tags, err := normalizeTags(it.Tags)
		if err != nil {
			return 0, err
		}
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM tasks WHERE id=?`, it.ID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return 0, err
		}
		if err := writeTaskTags(ctx, tx, it.ID, tags); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

// handleTagEdit: POST /api/tasks/tags/{rename|delete|restore}.
//
//	rename  {from, to} → {tag, tasks, merged, before}
//	delete  {tag}      → {tasks, before}
//	restore {before}   → {tasks}
func (s *Server) handleTagEdit(w http.ResponseWriter, r *http.Request, op string) {
	var b struct {
		From   string           `json:"from"`
		To     string           `json:"to"`
		Tag    string           `json:"tag"`
		Before []taskTagsBefore `json:"before"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&b); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ctx := r.Context()
	one := func(raw string) (string, bool) {
		t, err := normalizeTags([]string{raw})
		if err != nil || len(t) != 1 {
			return "", false
		}
		return t[0], true
	}
	switch op {
	case "rename", "delete":
		from, to := b.From, b.To
		if op == "delete" {
			from, to = b.Tag, ""
		}
		f, ok := one(from)
		if !ok {
			writeErr(w, 400, errBadTag.Error())
			return
		}
		t := ""
		if op == "rename" {
			if t, ok = one(to); !ok {
				writeErr(w, 400, errBadTag.Error())
				return
			}
			if t == f {
				writeErr(w, 400, "new name is the same")
				return
			}
		}
		res, err := s.editTagEverywhere(ctx, f, t)
		if errors.Is(err, errTagNotInUse) {
			writeErr(w, 404, err.Error())
			return
		}
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, res)
	case "restore":
		if len(b.Before) == 0 || len(b.Before) > maxTagRestore {
			writeErr(w, 400, "before (1-5000 tasks) required")
			return
		}
		n, err := s.restoreTaskTags(ctx, b.Before)
		if errors.Is(err, errBadTag) {
			writeErr(w, 400, err.Error())
			return
		}
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"tasks": n})
	default:
		writeErr(w, 404, "not found")
	}
}
