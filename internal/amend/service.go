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
	Op                 string   `json:"op"` // add|modify|delete
	Scope              string   `json:"scope"` // permanent|next_run
	TargetSpecItem     string   `json:"target_spec_item,omitempty"`
	Content            string   `json:"content"`
	Status             string   `json:"status"`
	ConflictWith       []string `json:"conflict_with,omitempty"`
	ConflictResolution string   `json:"conflict_resolution,omitempty"` // replace|coexist
	CreatedAt          string   `json:"created_at"`
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
	DB     *sql.DB
	Tasks  *tasksvc.Service
	Ver    *version.Service
	AI     Structurer
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
			ConflictWith: p.ConflictWith, CreatedAt: nowUTC(),
		}
		cw, _ := json.Marshal(p.ConflictWith)
		_, err := s.DB.ExecContext(ctx, `
INSERT INTO requirement_items(id, task_id, op, scope, target_spec_item, content, status, conflict_with, created_at)
VALUES(?,?,?,?,?,?,?,?,?)`,
			it.ID, it.TaskID, it.Op, it.Scope, nullEmpty(it.TargetSpecItem), it.Content, it.Status, string(cw), it.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	_ = actor
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
	q := `SELECT id, task_id, op, scope, COALESCE(target_spec_item,''), content, status,
		COALESCE(conflict_with,'[]'), COALESCE(conflict_resolution,''), created_at
		FROM requirement_items WHERE task_id=?`
	args := []any{taskID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var cw string
		if err := rows.Scan(&it.ID, &it.TaskID, &it.Op, &it.Scope, &it.TargetSpecItem, &it.Content, &it.Status, &cw, &it.ConflictResolution, &it.CreatedAt); err != nil {
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

// Confirm applies a pending item (permanent → new version; next_run → scheduled).
func (s *Service) Confirm(ctx context.Context, itemID, actor string) (Item, error) {
	it, err := s.get(ctx, itemID)
	if err != nil {
		return Item{}, err
	}
	if it.Status == "needs_decision" {
		return Item{}, fmt.Errorf("resolve conflict first (replace|coexist)")
	}
	if it.Status != "pending" {
		return Item{}, fmt.Errorf("item status %s cannot confirm", it.Status)
	}
	if actor == "" {
		actor = "user"
	}
	if it.Scope == "next_run" {
		exp := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
		_, err := s.DB.ExecContext(ctx, `
UPDATE requirement_items SET status='scheduled', decided_at=?, expires_at=? WHERE id=?`,
			nowUTC(), exp, itemID)
		if err != nil {
			return Item{}, err
		}
		it.Status = "scheduled"
		return it, nil
	}
	// permanent: merge into spec
	view, err := s.Tasks.Get(ctx, it.TaskID)
	if err != nil {
		return Item{}, err
	}
	spec := tasksvc.Spec{Prompt: view.Task.Prompt, Goal: view.Task.Prompt}
	if view.Spec != nil {
		spec = *view.Spec
	}
	spec, err = applyItem(spec, it)
	if err != nil {
		return Item{}, err
	}
	tv, err := s.Tasks.UpdateSpec(ctx, it.TaskID, spec, actor, "confirm amendment "+itemID)
	if err != nil {
		return Item{}, err
	}
	_, err = s.DB.ExecContext(ctx, `
UPDATE requirement_items SET status='applied', decided_at=?, applied_version_id=? WHERE id=?`,
		nowUTC(), tv.CurrentVersion.ID, itemID)
	if err != nil {
		return Item{}, err
	}
	it.Status = "applied"
	return it, nil
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
