package tasksvc

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func costPolicyFixture(t *testing.T) (*Service, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s := New(db.NewStore(d), version.New(d), nil)
	v, err := s.Create(context.Background(), CreateInput{Name: "test", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	return s, v.Task.ID
}
func TestCostPolicyAtomicRoundTrip(t *testing.T) {
	s, tid := costPolicyFixture(t)
	ctx := context.Background()
	p, err := s.GetCostPolicy(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	// Unlimited first: a new task has no per-run or daily cap of its own.
	if p.Mode != "task" || p.RunAmount != nil || p.DailyAmount != nil {
		t.Fatalf("default %+v", p)
	}
	var rows int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM budgets WHERE scope='task' AND scope_id=?`, tid).Scan(&rows)
	if rows != 0 {
		t.Fatalf("new task got %d budget rows", rows)
	}
	_, err = s.Store.DB.Exec(`INSERT INTO budgets(id,scope,scope_id,period,max_usd,max_tokens,action,warn_at_pct,created_at,updated_at) VALUES('d','task',?,'day',10,1000,'notify',75,'now','now')`, tid)
	if err != nil {
		t.Fatal(err)
	}
	mode := "bypass"
	amount := 20.0
	p, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{Mode: &mode, SetDailyAmount: true, DailyAmount: &amount, Currency: p.Currency, ExpectedRevision: p.Revision, ExpectedFXRevision: p.FXRevision})
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "bypass" || p.Revision != 2 || *p.DailyAmount != 20 || p.RunAmount != nil {
		t.Fatalf("updated %+v", p)
	}
	var tokens, warn int
	var action string
	s.Store.DB.QueryRow(`SELECT max_tokens,action,warn_at_pct FROM budgets WHERE id='d'`).Scan(&tokens, &action, &warn)
	if tokens != 1000 || action != "notify" || warn != 75 {
		t.Fatal("budget metadata overwritten")
	}
	amount = 30
	_, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{SetDailyAmount: true, DailyAmount: &amount, ExpectedRevision: 1})
	if !errors.Is(err, ErrCostPolicyConflict) {
		t.Fatalf("stale: %v", err)
	}
	p, _ = s.GetCostPolicy(ctx, tid)
	if *p.DailyAmount != 20 {
		t.Fatal("stale write applied")
	}
	// An omitted mode preserves bypass, while an explicit null clears a limit.
	p, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{SetRunAmount: true, ExpectedRevision: p.Revision})
	if err != nil || p.Mode != "bypass" || p.RunAmount != nil {
		t.Fatalf("clear %+v %v", p, err)
	}
	clone, err := s.Duplicate(ctx, tid, "clone", "user")
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.GetCostPolicy(ctx, clone.Task.ID)
	if err != nil || cp.Mode != "bypass" || cp.RunAmount != nil || *cp.DailyAmount != 20 {
		t.Fatalf("clone %+v %v", cp, err)
	}
}
func TestCostPolicyCurrencyAndValidation(t *testing.T) {
	s, tid := costPolicyFixture(t)
	ctx := context.Background()
	if _, err := s.Store.DB.Exec(`INSERT INTO budgets(id,scope,scope_id,period,max_usd,action,warn_at_pct,bucket,created_at,updated_at,currency) VALUES('r','task',?,'run',0.05,'pause_notify',80,'','now','now','USD')`, tid); err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetCostPolicy(ctx, tid)
	_, err := s.Store.DB.Exec(`INSERT INTO settings(user_id,key,value_json) VALUES('owner','pricing.fx','{"display":"CNY","usd_cny":7}')`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetCostPolicy(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if p.Currency != "CNY" || math.Abs(*p.RunAmount-.35) > 1e-10 {
		t.Fatalf("CNY %+v", p)
	}
	v := 100.0
	_, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{SetDailyAmount: true, DailyAmount: &v, ExpectedFXRevision: old.FXRevision})
	if !errors.Is(err, ErrCostPolicyConflict) {
		t.Fatalf("FX conflict %v", err)
	}
	p, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{SetDailyAmount: true, DailyAmount: &v, Currency: "CNY", ExpectedFXRevision: p.FXRevision})
	if err != nil {
		t.Fatal(err)
	}
	var stored float64
	var currency string
	s.Store.DB.QueryRow(`SELECT max_usd,currency FROM budgets WHERE scope_id=? AND period='day'`, tid).Scan(&stored, &currency)
	if stored != 100 || currency != "CNY" {
		t.Fatalf("stored %v %s", stored, currency)
	}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		_, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{SetRunAmount: true, RunAmount: &bad})
		if err == nil {
			t.Fatal("accepted invalid amount")
		}
	}
	invalid := "invalid"
	if _, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{Mode: &invalid}); err == nil {
		t.Fatal("invalid mode")
	}
}

func TestCostPolicyFailedAuditRollsBack(t *testing.T) {
	s, tid := costPolicyFixture(t)
	ctx := context.Background()
	before, _ := s.GetCostPolicy(ctx, tid)
	if _, err := s.Store.DB.Exec(`CREATE TRIGGER fail_cost_policy_audit BEFORE INSERT ON operation_log WHEN NEW.action='cost_policy_update' BEGIN SELECT RAISE(ABORT,'test audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	mode := "bypass"
	amount := 5.0
	if _, err := s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{Mode: &mode, SetRunAmount: true, RunAmount: &amount}); err == nil {
		t.Fatal("expected write failure")
	}
	after, err := s.GetCostPolicy(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode != before.Mode || after.Revision != before.Revision || after.RunAmount != nil || before.RunAmount != nil {
		t.Fatalf("partial save: %+v", after)
	}
}

func TestCostPolicyEditorLockAllowsNestedServiceOperations(t *testing.T) {
	s, tid := costPolicyFixture(t)
	ctx := context.Background()
	err := s.WithCostPolicyLock(ctx, func(ctx context.Context) error {
		created, err := s.Create(ctx, CreateInput{Name: "staged", Prompt: "hello", InitialStatus: "paused"})
		if err != nil {
			return err
		}
		if created.Task.Status != "paused" {
			t.Fatalf("unexpected staged status %q", created.Task.Status)
		}
		mode := CostModeBypass
		if _, err = s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{Mode: &mode}); err != nil {
			return err
		}
		if _, err = s.Duplicate(ctx, tid, "copy", "test"); err != nil {
			return err
		}
		return s.WithCostPolicyLock(ctx, func(ctx context.Context) error {
			_, err := s.UpdateCostPolicy(ctx, created.Task.ID, CostPolicyUpdate{Mode: &mode})
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	// The wrapper must release its lock, including on an error result.
	want := errors.New("editor failed")
	if err := s.WithCostPolicyLock(ctx, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if _, err := s.UpdateCostPolicy(ctx, tid, CostPolicyUpdate{}); err != nil {
		t.Fatal(err)
	}
}
