package tasksvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

// Migration 044 removed the per-run caps (USD 0.05) that tidebot itself had
// filled in, never the owner. The spend page tells the owner once, lists the
// tasks and can put every cap back exactly as it was (from operation_log).

const unlimitedNoticeKey = "cost.unlimited_notice"

// UnlimitedNoticeTask is a task whose implicit per-run cap was removed.
type UnlimitedNoticeTask struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// UnlimitedNotice is nil when there is nothing to tell (none removed, or the
// owner already dismissed the notice or restored the caps).
type UnlimitedNotice struct {
	Tasks []UnlimitedNoticeTask `json:"tasks"`
}

var ErrUnlimitedNoticeClosed = errors.New("default caps already restored or notice dismissed")

type removedCap struct {
	taskID, name, currency, action string
	amount                         float64
	warn                           int
}

func (s *Service) noticeState(ctx context.Context, q policyQuery) (string, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE user_id='owner' AND key=?`, unlimitedNoticeKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var v struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal([]byte(raw), &v)
	return v.State, nil
}

// removedCaps lists the migrated caps of tasks that still exist, oldest entry per task.
func removedCaps(ctx context.Context, q policyQuery) ([]removedCap, error) {
	rows, err := q.QueryContext(ctx, `SELECT o.entity_id, t.name, o.detail_json FROM operation_log o JOIN tasks t ON t.id=o.entity_id
WHERE o.entity_type='task' AND o.action='unlimited_default_migration' ORDER BY t.name, o.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []removedCap
	for rows.Next() {
		var c removedCap
		var raw string
		if err := rows.Scan(&c.taskID, &c.name, &raw); err != nil {
			return nil, err
		}
		var d struct {
			MaxAmount float64 `json:"max_amount"`
			Currency  string  `json:"currency"`
			Action    string  `json:"action"`
			WarnAtPct int     `json:"warn_at_pct"`
		}
		if json.Unmarshal([]byte(raw), &d) != nil || d.MaxAmount <= 0 || seen[c.taskID] {
			continue
		}
		seen[c.taskID] = true
		c.amount, c.currency, c.action, c.warn = d.MaxAmount, d.Currency, d.Action, d.WarnAtPct
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetUnlimitedNotice returns the one-time spend page notice, or nil.
func (s *Service) GetUnlimitedNotice(ctx context.Context) (*UnlimitedNotice, error) {
	st, err := s.noticeState(ctx, s.Store.DB)
	if err != nil || st != "" {
		return nil, err
	}
	caps, err := removedCaps(ctx, s.Store.DB)
	if err != nil || len(caps) == 0 {
		return nil, err
	}
	n := &UnlimitedNotice{Tasks: make([]UnlimitedNoticeTask, 0, len(caps))}
	for _, c := range caps {
		n.Tasks = append(n.Tasks, UnlimitedNoticeTask{ID: c.taskID, Name: c.name})
	}
	return n, nil
}

func setNoticeState(ctx context.Context, tx *sql.Tx, state string) error {
	raw, _ := json.Marshal(map[string]string{"state": state})
	_, err := tx.ExecContext(ctx, `INSERT INTO settings(user_id,key,value_json,updated_at) VALUES('owner',?,?,?)
ON CONFLICT(user_id,key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`, unlimitedNoticeKey, string(raw), nowUTC())
	return err
}

// DismissUnlimitedNotice hides the notice for good; the caps stay removed.
func (s *Service) DismissUnlimitedNotice(ctx context.Context) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = setNoticeState(ctx, tx, "dismissed"); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreUnlimitedDefaults writes every removed cap back as it was (converted to
// the display currency if that changed since). A task whose owner has set a
// per-run cap again keeps it. Returns how many caps came back.
func (s *Service) RestoreUnlimitedDefaults(ctx context.Context, actor string) (int, error) {
	if actor == "" {
		actor = "user"
	}
	unlock := s.lockCostPolicy(ctx)
	defer unlock()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	st, err := s.noticeState(ctx, tx)
	if err != nil {
		return 0, err
	}
	if st != "" {
		return 0, ErrUnlimitedNoticeClosed
	}
	caps, err := removedCaps(ctx, tx)
	if err != nil {
		return 0, err
	}
	f, _, err := policyFX(ctx, tx)
	if err != nil {
		return 0, err
	}
	now, n := nowUTC(), 0
	for _, c := range caps {
		var cur sql.NullFloat64
		err := tx.QueryRowContext(ctx, `SELECT max_usd FROM budgets WHERE scope='task' AND scope_id=? AND period='run' AND bucket=''`, c.taskID).Scan(&cur)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if cur.Valid && cur.Float64 > 0 {
			continue // the owner set a cap again since: theirs wins
		}
		from := c.currency
		if from == "" {
			from = f.Display
		}
		amount := f.Convert(c.amount, from, f.Display)
		action := c.action
		if action == "" {
			action = "pause_notify"
		}
		warn := c.warn
		if warn <= 0 {
			warn = 80
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO budgets(id,scope,scope_id,period,max_usd,action,warn_at_pct,bucket,created_at,updated_at,currency)
VALUES(?,'task',?,'run',?,?,?,'',?,?,?)
ON CONFLICT(scope,scope_id,period,bucket) DO UPDATE SET max_usd=excluded.max_usd,currency=excluded.currency,action=excluded.action,warn_at_pct=excluded.warn_at_pct,updated_at=excluded.updated_at`,
			id.New("bud"), c.taskID, amount, action, warn, now, now, f.Display); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE tasks SET cost_policy_revision=cost_policy_revision+1,updated_at=? WHERE id=?`, now, c.taskID); err != nil {
			return 0, err
		}
		detail, _ := json.Marshal(map[string]any{"max_amount": amount, "currency": f.Display})
		if _, err = tx.ExecContext(ctx, `INSERT INTO operation_log(at,entity_type,entity_id,action,actor,reason,detail_json) VALUES(?,'task',?,'unlimited_default_restore',?,'restore default per-run cap',?)`, now, c.taskID, actor, string(detail)); err != nil {
			return 0, err
		}
		n++
	}
	if err = setNoticeState(ctx, tx, "restored"); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("restore default caps: %w", err)
	}
	return n, nil
}
