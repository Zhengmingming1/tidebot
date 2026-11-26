package amend

import (
	"testing"
)

func TestMarkConsumedIsAtomicAndIdempotent(t *testing.T) {
	f, ctx := newFixture(t)
	items, err := f.svc.Propose(ctx, f.task, "!first;!second", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmBatch(ctx, items[0].MessageID, nil, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`
CREATE TRIGGER fail_second_consume BEFORE UPDATE ON requirement_items
WHEN OLD.id = '` + items[1].ID + `' AND NEW.status = 'consumed'
BEGIN
  SELECT RAISE(ABORT, 'forced consume failure');
END`); err != nil {
		t.Fatal(err)
	}
	ids := []string{items[0].ID, items[1].ID}
	if err := f.svc.MarkConsumed(ctx, "run_atomic", ids); err == nil {
		t.Fatal("consumption succeeded despite forced second-item failure")
	}
	for _, item := range items {
		if got, err := f.svc.get(ctx, item.ID); err != nil || got.Status != "scheduled" {
			t.Fatalf("failed consumption was not rolled back: %+v %v", got, err)
		}
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_second_consume`); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.MarkConsumed(ctx, "run_atomic", ids); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.MarkConsumed(ctx, "run_atomic", ids); err != nil {
		t.Fatalf("same-run recovery should be idempotent: %v", err)
	}
	if err := f.svc.MarkConsumed(ctx, "run_other", ids); err == nil {
		t.Fatal("items were consumed again for a different run")
	}
}

func TestRejectBatchRollsBackEveryItemOnFailure(t *testing.T) {
	f, ctx := newFixture(t)
	items, err := f.svc.Propose(ctx, f.task, "first;second", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`
CREATE TRIGGER fail_second_reject BEFORE UPDATE ON requirement_items
WHEN OLD.id = '` + items[1].ID + `' AND NEW.status = 'rejected'
BEGIN
  SELECT RAISE(ABORT, 'forced reject failure');
END`); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.RejectBatch(ctx, items[0].MessageID, nil); err == nil || n != 0 {
		t.Fatalf("batch reject returned n=%d err=%v", n, err)
	}
	for _, item := range items {
		if got, err := f.svc.get(ctx, item.ID); err != nil || got.Status != "pending" {
			t.Fatalf("failed rejection was not rolled back: %+v %v", got, err)
		}
	}
}
