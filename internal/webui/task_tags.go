package webui

import (
	"context"
	"database/sql"
	"errors"
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
