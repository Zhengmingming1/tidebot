package amend

import (
	"context"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

type fixedStructurer struct {
	proposal StructuredProposal
}

func (f fixedStructurer) Structure(context.Context, string, *tasksvc.Spec, string) (StructuredProposal, error) {
	return f.proposal, nil
}

func TestProposeRejectsWholeProposalBeforeWriting(t *testing.T) {
	f, ctx := newFixture(t)
	f.svc.AI = fixedStructurer{proposal: StructuredProposal{Items: []ProposedItem{
		{Op: "add", Scope: "permanent", Content: "valid"},
		{Op: "invalid", Scope: "permanent", Content: "invalid"},
	}}}
	if _, err := f.svc.Propose(ctx, f.task, "mixed proposal", "user"); err == nil {
		t.Fatal("invalid proposal was accepted")
	}
	for _, table := range []string{"amendment_messages", "requirement_items"} {
		var count int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("invalid proposal left %d rows in %s", count, table)
		}
	}
}

func TestConfirmItemsRollsBackClaimsWhenVersionCommitFails(t *testing.T) {
	f, ctx := newFixture(t)
	items, err := f.svc.Propose(ctx, f.task, "atomic", "user")
	if err != nil {
		t.Fatal(err)
	}
	v0, _ := f.counts(t)
	if _, err := f.db.Exec(`
CREATE TRIGGER fail_amendment_task_sync BEFORE UPDATE OF prompt ON tasks
WHEN OLD.id = '` + f.task + `'
BEGIN
  SELECT RAISE(FAIL, 'forced task sync failure');
END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmItems(ctx, []string{items[0].ID}, "user"); err == nil {
		t.Fatal("confirmation succeeded despite forced task sync failure")
	}
	v1, _ := f.counts(t)
	if v1 != v0 {
		t.Fatalf("failed confirmation created a version: before=%d after=%d", v0, v1)
	}
	item, err := f.svc.get(ctx, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "pending" {
		t.Fatalf("failed confirmation changed item status to %s", item.Status)
	}
}

func TestConcurrentConfirmItemsAppliesOnlyOnce(t *testing.T) {
	f, ctx := newFixture(t)
	items, err := f.svc.Propose(ctx, f.task, "only once", "user")
	if err != nil {
		t.Fatal(err)
	}
	v0, _ := f.counts(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := f.svc.ConfirmItems(ctx, []string{items[0].ID}, "user")
			errs <- err
		}()
	}
	close(start)
	succeeded := 0
	for i := 0; i < 2; i++ {
		if err := <-errs; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent confirmations succeeded %d times, want exactly one", succeeded)
	}
	v1, _ := f.counts(t)
	if v1-v0 != 1 {
		t.Fatalf("concurrent confirmations created %d versions, want one", v1-v0)
	}
	view, err := f.tasks.Get(ctx, f.task)
	if err != nil {
		t.Fatal(err)
	}
	if view.Spec == nil || len(view.Spec.Constraints) != 1 {
		t.Fatalf("amendment was not applied exactly once: %+v", view.Spec)
	}
	var status, versionID string
	if err := f.db.QueryRow(`SELECT status, COALESCE(applied_version_id,'') FROM requirement_items WHERE id=?`, items[0].ID).Scan(&status, &versionID); err != nil {
		t.Fatal(err)
	}
	if status != "applied" || versionID == "" {
		t.Fatalf("unexpected confirmed item state: status=%s version=%s", status, versionID)
	}
}
