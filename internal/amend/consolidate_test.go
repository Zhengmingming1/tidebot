package amend

import (
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func TestPlanConsolidation(t *testing.T) {
	cs := []tasksvc.SpecConstraint{
		{ID: "c1", Text: "Keep backups for 10 days", Source: "req_a"},
		{ID: "c2", Text: "keep backups for 10 days.", Source: "req_b"},
		{ID: "c3", Text: "Skip node_modules", Source: "req_c"},
		{ID: "c4", Text: "Skip node_modules and .cache folders", Source: "req_d"},
		{ID: "c5", Text: "Send a short summary"},
	}
	p := PlanConsolidation("t", cs)
	if len(p.After) != 3 || len(p.Changes) != 2 {
		t.Fatalf("%+v", p)
	}
	if p.After[0].Source != "req_a,req_b" || p.After[1].ID != "c3" || p.After[1].Text != "Skip node_modules and .cache folders" || p.After[1].Source != "req_c,req_d" {
		t.Fatalf("%+v", p.After)
	}
	if PlanConsolidation("t", p.After).Hash == p.Hash {
		t.Fatal("hash must change")
	}
}

func TestApplyConsolidation(t *testing.T) {
	f, ctx := newFixture(t)
	items, _ := f.svc.Propose(ctx, f.task, "keep logs;Keep logs.;b", "user")
	if _, err := f.svc.ConfirmBatch(ctx, items[0].MessageID, nil, "user"); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Consolidation(ctx, f.task)
	if err != nil || len(p.Changes) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := f.svc.ApplyConsolidation(ctx, f.task, "stale", "user"); err == nil {
		t.Fatal("hash check")
	}
	vid, err := f.svc.ApplyConsolidation(ctx, f.task, p.Hash, "user")
	if err != nil || vid == "" {
		t.Fatal(err)
	}
	view, _ := f.tasks.Get(ctx, f.task)
	if len(view.Spec.Constraints) != 2 {
		t.Fatal(view.Spec.Constraints)
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM requirement_items WHERE status='archived'`).Scan(&n)
	if n != 3 {
		t.Fatal("archived", n)
	}
}
