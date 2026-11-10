package tasksvc

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestUnlimitedNoticeRestoreAndDismiss(t *testing.T) {
	s, a := costPolicyFixture(t)
	ctx := context.Background()
	if n, err := s.GetUnlimitedNotice(ctx); err != nil || n != nil {
		t.Fatalf("nothing removed yet: %+v %v", n, err)
	}
	b, _ := s.Create(ctx, CreateInput{Name: "b", Prompt: "x"})
	mig := func(task string, amount float64, cur string) {
		if _, err := s.Store.DB.Exec(`INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES('now','task',?,'unlimited_default_migration','system',json_object('max_amount',?,'currency',?,'action','pause_notify','warn_at_pct',70))`, task, amount, cur); err != nil {
			t.Fatal(err)
		}
	}
	mig(a, 0.05, "USD")
	mig(b.Task.ID, 0.05, "USD")
	n, err := s.GetUnlimitedNotice(ctx)
	if err != nil || n == nil || len(n.Tasks) != 2 {
		t.Fatalf("notice %+v %v", n, err)
	}
	// The owner set b's cap again after the upgrade: restoring keeps theirs.
	mine := 2.0
	if _, err = s.UpdateCostPolicy(ctx, b.Task.ID, CostPolicyUpdate{SetRunAmount: true, RunAmount: &mine}); err != nil {
		t.Fatal(err)
	}
	// The display currency changed since: the restored cap is converted.
	if _, err = s.Store.DB.Exec(`INSERT INTO settings(user_id,key,value_json) VALUES('owner','pricing.fx','{"display":"CNY","usd_cny":7}')`); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetCostPolicy(ctx, a)
	got, err := s.RestoreUnlimitedDefaults(ctx, "test")
	if err != nil || got != 1 {
		t.Fatalf("restored %d %v", got, err)
	}
	p, _ := s.GetCostPolicy(ctx, a)
	if p.RunAmount == nil || math.Abs(*p.RunAmount-0.35) > 1e-9 || p.Revision != before.Revision+1 {
		t.Fatalf("a %+v", p)
	}
	var warn int
	s.Store.DB.QueryRow(`SELECT warn_at_pct FROM budgets WHERE scope_id=? AND period='run'`, a).Scan(&warn)
	if warn != 70 {
		t.Fatalf("warn %d", warn)
	}
	pb, _ := s.GetCostPolicy(ctx, b.Task.ID)
	if pb.RunAmount == nil || math.Abs(*pb.RunAmount-2*7) > 1e-9 {
		t.Fatalf("b %+v", pb)
	}
	if n, _ = s.GetUnlimitedNotice(ctx); n != nil {
		t.Fatal("notice after restore")
	}
	if _, err = s.RestoreUnlimitedDefaults(ctx, "test"); !errors.Is(err, ErrUnlimitedNoticeClosed) {
		t.Fatalf("second restore %v", err)
	}
}

func TestUnlimitedNoticeDismissKeepsCapsRemoved(t *testing.T) {
	s, a := costPolicyFixture(t)
	ctx := context.Background()
	if _, err := s.Store.DB.Exec(`INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES('now','task',?,'unlimited_default_migration','system','{"max_amount":0.05,"currency":"USD"}')`, a); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissUnlimitedNotice(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.GetUnlimitedNotice(ctx); n != nil {
		t.Fatal("dismissed notice shown")
	}
	if p, _ := s.GetCostPolicy(ctx, a); p.RunAmount != nil {
		t.Fatalf("dismiss changed caps %+v", p)
	}
	if _, err := s.RestoreUnlimitedDefaults(ctx, "test"); !errors.Is(err, ErrUnlimitedNoticeClosed) {
		t.Fatal(err)
	}
}
