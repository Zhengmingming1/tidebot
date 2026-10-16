package webui

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestSpendBreakdown(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	day := time.Now().In(c.s.deps.Cost.Location()).Format("2006-01-02")
	_, _ = c.s.deps.DB.ExecContext(ctx, `INSERT INTO runs(id,status,provider_name,model,created_at) VALUES('sr1','succeeded','kimi','k2','2026-10-16T00:00:00Z'),('sr2','succeeded','anthropic','sonnet','2026-10-16T00:00:00Z')`)
	_, _ = c.s.deps.DB.ExecContext(ctx, `INSERT INTO cost_ledger(run_id,bucket,usd,tokens,day,month,created_at) VALUES('sr1','production',0.3,100,?,?,?),('sr2','production',0.1,50,?,?,?)`,
		day, day[:7], time.Now().UTC().Format(time.RFC3339), day, day[:7], time.Now().UTC().Format(time.RFC3339))
	code, out := c.json(http.MethodGet, "/api/spend/breakdown?range=7d&by=provider", nil)
	rows, _ := out["rows"].([]any)
	if code != 200 || len(rows) != 2 || rows[0].(map[string]any)["key"] != "kimi" {
		t.Fatalf("by provider %d %v", code, out)
	}
	if code, out := c.json(http.MethodGet, "/api/spend/breakdown?range=all&by=model", nil); code != 200 || out["total_usd"].(float64) < 0.39 {
		t.Fatalf("by model %d %v", code, out)
	}
	for _, q := range []string{"range=1y", "by=user", "by=model%3Bdrop"} {
		if code, _ := c.json(http.MethodGet, "/api/spend/breakdown?"+q, nil); code != 400 {
			t.Fatalf("%s accepted: %d", q, code)
		}
	}
}

func TestBudgetActionAndWarn(t *testing.T) {
	c := login(t)
	if code, _ := c.json(http.MethodPost, "/api/budgets", map[string]any{"scope": "global", "period": "day", "max_usd": 5, "action": "block_notify", "warn_at_pct": 90}); code != 200 {
		t.Fatalf("save %d", code)
	}
	sum, _ := c.s.deps.Cost.Summary(context.Background(), "")
	found := false
	for _, b := range sum.Budgets {
		if b.Scope == "global" && b.Period == "day" {
			found = b.Action == "block_notify" && b.WarnAtPct == 90
		}
	}
	if !found {
		t.Fatalf("not saved: %+v", sum.Budgets)
	}
	if code, _ := c.json(http.MethodPost, "/api/budgets", map[string]any{"scope": "global", "period": "day", "max_usd": 5, "action": "delete_everything"}); code != 400 {
		t.Fatalf("bad action %d", code)
	}
}
