package tasksvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

const (
	CostModeTask   = "task"
	CostModeGlobal = "global"
	CostModeBypass = "bypass"
)

// ErrCostPolicyConflict means either the task policy or the currency changed.
var ErrCostPolicyConflict = errors.New("cost policy or currency settings changed; reload before saving")

type CostPolicy struct {
	Mode        string   `json:"mode"`
	RunAmount   *float64 `json:"run_amount"`
	DailyAmount *float64 `json:"daily_amount"`
	Currency    string   `json:"currency"`
	Revision    int64    `json:"revision"`
	FXRevision  string   `json:"fx_revision"`
}

type CostPolicyUpdate struct {
	Mode                         *string
	RunAmount, DailyAmount       *float64
	SetRunAmount, SetDailyAmount bool
	Currency                     string
	ExpectedRevision             int64
	ExpectedFXRevision           string
	Actor                        string
}

type policyQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func policyFX(ctx context.Context, q policyQuery) (pricing.FX, string, error) {
	f := pricing.DefaultFX
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE user_id='owner' AND key='pricing.fx'`).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return f, "", err
	}
	if err == nil {
		if json.Unmarshal([]byte(raw), &f) != nil || f.Validate() != nil {
			f = pricing.DefaultFX
		}
	}
	encoded, _ := json.Marshal(f)
	return f, string(encoded), nil
}

func readCostPolicy(ctx context.Context, q policyQuery, taskID string) (CostPolicy, error) {
	var p CostPolicy
	if err := q.QueryRowContext(ctx, `SELECT cost_control_mode,cost_policy_revision FROM tasks WHERE id=?`, taskID).Scan(&p.Mode, &p.Revision); err != nil {
		return p, err
	}
	f, revision, err := policyFX(ctx, q)
	if err != nil {
		return p, err
	}
	p.Currency, p.FXRevision = f.Display, revision
	rows, err := q.QueryContext(ctx, `SELECT period,max_usd,currency FROM budgets WHERE scope='task' AND scope_id=? AND bucket='' AND period IN ('run','day')`, taskID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var period, currency string
		var amount sql.NullFloat64
		if err := rows.Scan(&period, &amount, &currency); err != nil {
			return p, err
		}
		if amount.Valid && amount.Float64 > 0 {
			v := f.Convert(amount.Float64, currency, f.Display)
			if period == "run" {
				p.RunAmount = &v
			} else {
				p.DailyAmount = &v
			}
		}
	}
	return p, rows.Err()
}

// GetCostPolicy returns amounts in the currently selected currency. It does not lock,
// allowing launch callers holding db.CostPolicyMutex to use the same snapshot boundary.
func (s *Service) GetCostPolicy(ctx context.Context, taskID string) (CostPolicy, error) {
	return readCostPolicy(ctx, s.Store.DB, taskID)
}

type costPolicyLockContextKey struct{}

// WithCostPolicyLock serializes a synchronous editor operation with policy saves,
// currency changes and run admission. Use the supplied context only inside fn;
// it must not escape to goroutines or outlive this call.
func (s *Service) WithCostPolicyLock(ctx context.Context, fn func(context.Context) error) error {
	unlock := s.lockCostPolicy(ctx)
	defer unlock()
	return fn(context.WithValue(ctx, costPolicyLockContextKey{}, s.Store.DB))
}

func (s *Service) lockCostPolicy(ctx context.Context) func() {
	if ctx.Value(costPolicyLockContextKey{}) == s.Store.DB {
		return func() {}
	}
	mu := db.CostPolicyMutex(s.Store.DB)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) UpdateCostPolicy(ctx context.Context, taskID string, in CostPolicyUpdate) (CostPolicy, error) {
	unlock := s.lockCostPolicy(ctx)
	defer unlock()
	return s.UpdateCostPolicyLocked(ctx, taskID, in)
}

// UpdateCostPolicyLocked updates a policy while the caller already holds
// db.CostPolicyMutex(s.Store.DB). This allows a task editor to keep its policy
// revision check and accompanying spec edits in the same serialization boundary.
func (s *Service) UpdateCostPolicyLocked(ctx context.Context, taskID string, in CostPolicyUpdate) (CostPolicy, error) {
	if in.Mode != nil && *in.Mode != CostModeTask && *in.Mode != CostModeGlobal && *in.Mode != CostModeBypass {
		return CostPolicy{}, fmt.Errorf("invalid cost control mode")
	}
	for _, amount := range []*float64{in.RunAmount, in.DailyAmount} {
		if amount != nil && (math.IsNaN(*amount) || math.IsInf(*amount, 0) || *amount < 0) {
			return CostPolicy{}, fmt.Errorf("budget amount must be finite and nonnegative")
		}
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return CostPolicy{}, err
	}
	defer tx.Rollback()
	before, err := readCostPolicy(ctx, tx, taskID)
	if err != nil {
		return CostPolicy{}, err
	}
	if in.ExpectedRevision != 0 && in.ExpectedRevision != before.Revision {
		return CostPolicy{}, ErrCostPolicyConflict
	}
	if in.ExpectedFXRevision != "" && in.ExpectedFXRevision != before.FXRevision {
		return CostPolicy{}, ErrCostPolicyConflict
	}
	if in.Currency != "" && in.Currency != before.Currency {
		return CostPolicy{}, ErrCostPolicyConflict
	}
	mode := before.Mode
	if in.Mode != nil {
		mode = *in.Mode
	}
	now := nowUTC()
	for _, update := range []struct {
		period string
		set    bool
		amount *float64
	}{{"run", in.SetRunAmount, in.RunAmount}, {"day", in.SetDailyAmount, in.DailyAmount}} {
		if !update.set {
			continue
		}
		var amount any
		if update.amount != nil && *update.amount > 0 {
			amount = *update.amount
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO budgets(id,scope,scope_id,period,max_usd,action,warn_at_pct,bucket,created_at,updated_at,currency)
VALUES(?,'task',?,?,?,'pause_notify',80,'',?,?,?)
ON CONFLICT(scope,scope_id,period,bucket) DO UPDATE SET max_usd=excluded.max_usd,currency=excluded.currency,updated_at=excluded.updated_at`, id.New("bud"), taskID, update.period, amount, now, now, before.Currency)
		if err != nil {
			return CostPolicy{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET cost_control_mode=?,cost_policy_revision=cost_policy_revision+1,updated_at=? WHERE id=?`, mode, now, taskID); err != nil {
		return CostPolicy{}, err
	}
	after, err := readCostPolicy(ctx, tx, taskID)
	if err != nil {
		return CostPolicy{}, err
	}
	detail, _ := json.Marshal(struct{ Before, After CostPolicy }{before, after})
	actor := in.Actor
	if actor == "" {
		actor = "user"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO operation_log(at,entity_type,entity_id,action,actor,reason,detail_json) VALUES(?,'task',?,'cost_policy_update',?,'update task cost policy',?)`, now, taskID, actor, string(detail)); err != nil {
		return CostPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return CostPolicy{}, err
	}
	return after, nil
}

func (s *Service) duplicateCostPolicy(ctx context.Context, source, target, actor string) error {
	unlock := s.lockCostPolicy(ctx)
	defer unlock()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET cost_control_mode=(SELECT cost_control_mode FROM tasks WHERE id=?) WHERE id=?`, source, target)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO budgets(id,scope,scope_id,period,max_usd,max_tokens,action,warn_at_pct,bucket,created_at,updated_at,currency)
SELECT ? || '-' || id,scope,?,period,max_usd,max_tokens,action,warn_at_pct,bucket,?,?,currency FROM budgets WHERE scope='task' AND scope_id=?`, target, target, nowUTC(), nowUTC(), source)
	if err != nil {
		return err
	}
	return tx.Commit()
}
