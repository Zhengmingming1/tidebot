// Package amend implements requirement amendment propose/confirm flow (D3 subset).
package amend

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Item is a structured requirement amendment.
type Item struct {
	ID                 string   `json:"id"`
	TaskID             string   `json:"task_id"`
	Op                 string   `json:"op"`    // add|modify|delete
	Scope              string   `json:"scope"` // permanent|next_run
	TargetSpecItem     string   `json:"target_spec_item,omitempty"`
	Content            string   `json:"content"`
	Status             string   `json:"status"`
	ConflictWith       []string `json:"conflict_with,omitempty"`
	ConflictResolution string   `json:"conflict_resolution,omitempty"` // replace|coexist
	CreatedAt          string   `json:"created_at"`
	MessageID          string   `json:"message_id,omitempty"`
	Utterance          string   `json:"utterance,omitempty"`
}

// StructuredProposal is AI (or mock) output.
type StructuredProposal struct {
	Items []ProposedItem `json:"items"`
}

type ProposedItem struct {
	Op             string   `json:"op"`
	Scope          string   `json:"scope"`
	TargetSpecItem string   `json:"target_spec_item,omitempty"`
	Content        string   `json:"content"`
	ConflictWith   []string `json:"conflict_with,omitempty"`
}

// Structurer turns natural language into structured items.
type Structurer interface {
	Structure(ctx context.Context, taskID string, spec *tasksvc.Spec, utterance string) (StructuredProposal, error)
}

// Service stores and confirms amendments.
type Service struct {
	DB    *sql.DB
	Tasks *tasksvc.Service
	Ver   *version.Service
	AI    Structurer
	// OnProposed, if set, is called after new requirement items were stored and await confirmation.
	OnProposed func(taskID string, items []Item)
}

func New(db *sql.DB, tasks *tasksvc.Service, ver *version.Service, ai Structurer) *Service {
	return &Service{DB: db, Tasks: tasks, Ver: ver, AI: ai}
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// Propose structures an utterance into pending items.
func (s *Service) Propose(ctx context.Context, taskID, utterance, actor string) ([]Item, error) {
	view, err := s.Tasks.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if s.AI == nil {
		return nil, fmt.Errorf("no amendment structurer configured")
	}
	prop, err := s.AI.Structure(ctx, taskID, view.Spec, utterance)
	if err != nil {
		return nil, err
	}
	msgID := id.New("msg")
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO amendment_messages(id, task_id, utterance, created_at) VALUES(?,?,?,?)`,
		msgID, taskID, utterance, nowUTC()); err != nil {
		return nil, err
	}
	var out []Item
	for _, p := range prop.Items {
		if err := validateProposed(p); err != nil {
			return nil, err
		}
		status := "pending"
		if len(p.ConflictWith) > 0 {
			status = "needs_decision"
		}
		it := Item{
			ID: id.New("req"), TaskID: taskID, Op: p.Op, Scope: p.Scope,
			TargetSpecItem: p.TargetSpecItem, Content: p.Content, Status: status,
			ConflictWith: p.ConflictWith, CreatedAt: nowUTC(), MessageID: msgID, Utterance: utterance,
		}
		cw, _ := json.Marshal(p.ConflictWith)
		_, err := s.DB.ExecContext(ctx, `
INSERT INTO requirement_items(id, task_id, message_id, op, scope, target_spec_item, content, status, conflict_with, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
			it.ID, it.TaskID, msgID, it.Op, it.Scope, nullEmpty(it.TargetSpecItem), it.Content, it.Status, string(cw), it.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	_ = actor
	if s.OnProposed != nil && len(out) > 0 {
		s.OnProposed(taskID, out)
	}
	return out, nil
}

func validateProposed(p ProposedItem) error {
	switch p.Op {
	case "add", "modify", "delete":
	default:
		return fmt.Errorf("invalid op %q", p.Op)
	}
	switch p.Scope {
	case "permanent", "next_run":
	default:
		return fmt.Errorf("invalid scope %q", p.Scope)
	}
	if strings.TrimSpace(p.Content) == "" && p.Op != "delete" {
		return fmt.Errorf("content required")
	}
	return nil
}

func nullEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// List returns items for a task, optionally filtered by status.
func (s *Service) List(ctx context.Context, taskID, status string) ([]Item, error) {
	q := `SELECT r.id, r.task_id, r.op, r.scope, COALESCE(r.target_spec_item,''), r.content, r.status,
		COALESCE(r.conflict_with,'[]'), COALESCE(r.conflict_resolution,''), r.created_at,
		COALESCE(r.message_id,''), COALESCE(m.utterance,'')
		FROM requirement_items r LEFT JOIN amendment_messages m ON m.id = r.message_id WHERE r.task_id=?`
	args := []any{taskID}
	if status != "" {
		q += ` AND r.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY r.created_at, r.id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var cw string
		if err := rows.Scan(&it.ID, &it.TaskID, &it.Op, &it.Scope, &it.TargetSpecItem, &it.Content, &it.Status, &cw, &it.ConflictResolution, &it.CreatedAt, &it.MessageID, &it.Utterance); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cw), &it.ConflictWith)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ResolveConflict sets replace|coexist and returns item to pending.
func (s *Service) ResolveConflict(ctx context.Context, itemID, resolution string) error {
	if resolution != "replace" && resolution != "coexist" {
		return fmt.Errorf("resolution must be replace or coexist")
	}
	res, err := s.DB.ExecContext(ctx, `
UPDATE requirement_items SET conflict_resolution=?, status='pending', decided_at=?
WHERE id=? AND status='needs_decision'`, resolution, nowUTC(), itemID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("item not in needs_decision")
	}
	return nil
}

// Confirm applies a single pending item (permanent → new version; next_run → scheduled).
func (s *Service) Confirm(ctx context.Context, itemID, actor string) (Item, error) {
	res, err := s.ConfirmItems(ctx, []string{itemID}, actor)
	if err != nil {
		return Item{}, err
	}
	return res.Items[0], nil
}

// BatchResult is the outcome of confirming several items together.
type BatchResult struct {
	Items     []Item `json:"items"`
	VersionID string `json:"version_id,omitempty"` // empty when no permanent item was included
	Excluded  []Item `json:"excluded,omitempty"`
}

// ConfirmBatch confirms the items of one amendment message together. amendmentID may be
// the message id or the id of any item in it. include (optional) limits the batch to those
// item ids; unselected items stay pending. Conflicts among selected items must be resolved
// first. All permanent items produce exactly ONE new task version.
func (s *Service) ConfirmBatch(ctx context.Context, amendmentID string, include []string, actor string) (BatchResult, error) {
	msgID := amendmentID
	var tmp string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(message_id,'') FROM requirement_items WHERE id=?`, amendmentID).Scan(&tmp); err == nil && tmp != "" {
		msgID = tmp
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM requirement_items WHERE message_id=? AND status IN ('pending','needs_decision') ORDER BY created_at, id`, msgID)
	if err != nil {
		return BatchResult{}, err
	}
	var all []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			rows.Close()
			return BatchResult{}, err
		}
		all = append(all, x)
	}
	rows.Close()
	if len(all) == 0 {
		return BatchResult{}, fmt.Errorf("no pending items for amendment %s", amendmentID)
	}
	want := map[string]bool{}
	for _, x := range include {
		want[x] = true
	}
	var sel, excl []string
	for _, x := range all {
		if len(include) == 0 || want[x] {
			sel = append(sel, x)
		} else {
			excl = append(excl, x)
		}
	}
	for x := range want {
		found := false
		for _, y := range all {
			if y == x {
				found = true
			}
		}
		if !found {
			return BatchResult{}, fmt.Errorf("item %s is not pending in amendment %s", x, amendmentID)
		}
	}
	if len(sel) == 0 {
		return BatchResult{}, fmt.Errorf("no items selected")
	}
	res, err := s.ConfirmItems(ctx, sel, actor)
	if err != nil {
		return res, err
	}
	for _, x := range excl {
		if it, err := s.get(ctx, x); err == nil {
			res.Excluded = append(res.Excluded, it)
		}
	}
	return res, nil
}

// ConfirmItems confirms several pending items of one task atomically: all permanent items
// are merged into the spec as a single new version (one pointer move, one oplog entry);
// next_run items are scheduled and never create a version.
func (s *Service) ConfirmItems(ctx context.Context, itemIDs []string, actor string) (BatchResult, error) {
	if actor == "" {
		actor = "user"
	}
	var items []Item
	for _, iid := range itemIDs {
		it, err := s.get(ctx, iid)
		if err != nil {
			return BatchResult{}, fmt.Errorf("item %s: %w", iid, err)
		}
		if it.Status == "needs_decision" {
			return BatchResult{}, fmt.Errorf("item %s has an unresolved conflict; resolve it first (replace|coexist)", iid)
		}
		if it.Status != "pending" {
			return BatchResult{}, fmt.Errorf("item %s status %s cannot confirm", iid, it.Status)
		}
		if len(items) > 0 && it.TaskID != items[0].TaskID {
			return BatchResult{}, fmt.Errorf("items belong to different tasks")
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return BatchResult{}, fmt.Errorf("no items")
	}
	var perm []int
	for i, it := range items {
		if it.Scope == "permanent" {
			perm = append(perm, i)
		}
	}
	var out BatchResult
	if len(perm) > 0 {
		view, err := s.Tasks.Get(ctx, items[0].TaskID)
		if err != nil {
			return BatchResult{}, err
		}
		spec := tasksvc.Spec{Prompt: view.Task.Prompt, Goal: view.Task.Prompt}
		if view.Spec != nil {
			spec = *view.Spec
		}
		ids := make([]string, 0, len(perm))
		for _, i := range perm {
			spec, err = applyItem(spec, items[i])
			if err != nil {
				return BatchResult{}, err
			}
			ids = append(ids, items[i].ID)
		}
		msg := "confirm amendment " + strings.Join(ids, ",")
		tv, err := s.Tasks.UpdateSpec(ctx, items[0].TaskID, spec, actor, msg)
		if err != nil {
			return BatchResult{}, err
		}
		out.VersionID = tv.CurrentVersion.ID
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now := nowUTC()
	exp := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	for i := range items {
		if items[i].Scope == "next_run" {
			_, err = tx.ExecContext(ctx, `UPDATE requirement_items SET status='scheduled', decided_at=?, expires_at=? WHERE id=?`, now, exp, items[i].ID)
			items[i].Status = "scheduled"
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE requirement_items SET status='applied', decided_at=?, applied_version_id=? WHERE id=?`, now, out.VersionID, items[i].ID)
			items[i].Status = "applied"
		}
		if err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	out.Items = items
	return out, nil
}

// RejectBatch rejects all open items of an amendment message (or the given subset).
func (s *Service) RejectBatch(ctx context.Context, amendmentID string, include []string) (int, error) {
	msgID := amendmentID
	var tmp string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(message_id,'') FROM requirement_items WHERE id=?`, amendmentID).Scan(&tmp); err == nil && tmp != "" {
		msgID = tmp
	}
	want := map[string]bool{}
	for _, x := range include {
		want[x] = true
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM requirement_items WHERE message_id=? AND status IN ('pending','needs_decision')`, msgID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var x string
		if rows.Scan(&x) == nil && (len(include) == 0 || want[x]) {
			ids = append(ids, x)
		}
	}
	rows.Close()
	for _, x := range ids {
		if err := s.Reject(ctx, x); err != nil {
			return 0, err
		}
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("nothing to reject")
	}
	return len(ids), nil
}

// Reject marks item rejected.
func (s *Service) Reject(ctx context.Context, itemID string) error {
	res, err := s.DB.ExecContext(ctx, `
UPDATE requirement_items SET status='rejected', decided_at=? WHERE id=? AND status IN ('pending','needs_decision')`,
		nowUTC(), itemID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("item not rejectable")
	}
	return nil
}

// MarkConsumed marks scheduled items used by a run.
func (s *Service) MarkConsumed(ctx context.Context, runID string, itemIDs []string) error {
	for _, iid := range itemIDs {
		_, err := s.DB.ExecContext(ctx, `
UPDATE requirement_items SET status='consumed', consumed_run_id=? WHERE id=? AND status='scheduled'`,
			runID, iid)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) get(ctx context.Context, itemID string) (Item, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, task_id, op, scope, COALESCE(target_spec_item,''), content, status,
  COALESCE(conflict_with,'[]'), COALESCE(conflict_resolution,''), created_at
FROM requirement_items WHERE id=?`, itemID)
	var it Item
	var cw string
	if err := row.Scan(&it.ID, &it.TaskID, &it.Op, &it.Scope, &it.TargetSpecItem, &it.Content, &it.Status, &cw, &it.ConflictResolution, &it.CreatedAt); err != nil {
		return Item{}, err
	}
	_ = json.Unmarshal([]byte(cw), &it.ConflictWith)
	return it, nil
}

func applyItem(spec tasksvc.Spec, it Item) (tasksvc.Spec, error) {
	switch it.Op {
	case "add":
		cid := it.TargetSpecItem
		if cid == "" {
			cid = id.New("c")
		}
		spec.Constraints = append(spec.Constraints, tasksvc.SpecConstraint{ID: cid, Text: it.Content, Source: it.ID})
	case "modify":
		found := false
		for i := range spec.Constraints {
			if spec.Constraints[i].ID == it.TargetSpecItem || (it.TargetSpecItem == "" && i == 0) {
				if it.ConflictResolution == "replace" || it.TargetSpecItem != "" {
					spec.Constraints[i].Text = it.Content
					found = true
					break
				}
			}
		}
		if !found {
			// treat as add if target missing and coexist
			spec.Constraints = append(spec.Constraints, tasksvc.SpecConstraint{ID: id.New("c"), Text: it.Content, Source: it.ID})
		}
	case "delete":
		var kept []tasksvc.SpecConstraint
		for _, c := range spec.Constraints {
			if c.ID == it.TargetSpecItem {
				continue
			}
			kept = append(kept, c)
		}
		spec.Constraints = kept
	default:
		return spec, fmt.Errorf("unknown op")
	}
	return spec, nil
}
