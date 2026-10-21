package webui

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestNiceUpAndSimilarity(t *testing.T) {
	for in, want := range map[float64]float64{0.031: 0.05, 0.3: 0.5, 0.2: 0.2, 1.2: 2, 2.1: 2.5, 7: 10, 0: 0} {
		if got := niceUp(in); got != want {
			t.Errorf("niceUp(%v)=%v want %v", in, got, want)
		}
	}
	a, b := terms("每天汇总 GitHub 提交日报"), terms("GitHub 日报")
	if similarity(a, b) < 0.5 {
		t.Fatalf("zh/en terms %v %v", a, b)
	}
	if similarity(terms("清理日志"), terms("Weekly digest of PRs")) != 0 {
		t.Fatal("unrelated")
	}
	if !isHeavy("Refactor the billing module") || isHeavy("summarize news") {
		t.Fatal("heavy")
	}
}

func budgetOf(t *testing.T, c *client, body map[string]any) map[string]any {
	t.Helper()
	code, out := c.json(http.MethodPost, "/api/tasks/infer", body)
	b, _ := out["budget"].(map[string]any)
	if code != 200 || b == nil {
		t.Fatalf("infer %d %v", code, out)
	}
	return b
}

func TestSuggestBudget(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	// No provider: no budget block at all.
	if _, out := c.json(http.MethodPost, "/api/tasks/infer", map[string]any{"prompt": "每天汇总新闻"}); out["budget"] != nil {
		t.Fatalf("budget without provider %v", out)
	}
	// Metered provider, no history: prices × a typical run.
	b := budgetOf(t, c, map[string]any{"prompt": "每天汇总新闻", "provider": "deepseek"})
	if b["basis"] != "price" || b["amount"].(float64) <= 0 || b["estimate"].(float64) <= 0 || b["amount"].(float64) < b["estimate"].(float64) {
		t.Fatalf("price basis %v", b)
	}
	light := b["amount"].(float64)
	b = budgetOf(t, c, map[string]any{"prompt": "重构整个项目的计费模块并写测试", "provider": "deepseek"})
	if b["heavy"] != true || b["amount"].(float64) <= light {
		t.Fatalf("heavy %v vs %v", b, light)
	}
	// Flat plan: no cap.
	if b = budgetOf(t, c, map[string]any{"prompt": "x", "provider": "kimi-coding"}); b["basis"] != "subscription" || b["amount"].(float64) != 0 {
		t.Fatalf("subscription %v", b)
	}
	if b = budgetOf(t, c, map[string]any{"prompt": "x", "provider": "nope"}); b["basis"] != "none" {
		t.Fatalf("unknown provider %v", b)
	}
	// History of a similar task wins.
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "GitHub 日报", "prompt": "汇总昨天的 GitHub 提交，写成日报", "provider": "deepseek"})
	tid := tk["id"].(string)
	ts := time.Now().UTC().Format(time.RFC3339)
	for i, amt := range []float64{0.10, 0.12, 0.20, 0.15} {
		if _, err := c.s.deps.DB.ExecContext(ctx, `INSERT INTO runs(id,task_id,status,provider_name,model,created_at,cost_amount,cost_currency,cost_source) VALUES(?,?,'succeeded','deepseek','deepseek-chat',?,?,'USD','priced')`,
			fmt.Sprintf("bh%d", i), tid, ts, amt); err != nil {
			t.Fatal(err)
		}
	}
	b = budgetOf(t, c, map[string]any{"prompt": "每天早上汇总 GitHub 提交，写成日报发到群里", "provider": "deepseek"})
	f := c.s.fx().Factor("USD", c.s.fx().Display)
	if b["basis"] != "similar" || b["runs"].(float64) != 4 || b["amount"].(float64) < 0.2*f*1.5-1e-9 {
		t.Fatalf("similar %v", b)
	}
	if sim, _ := b["similar"].([]any); len(sim) != 1 || sim[0] != "GitHub 日报" {
		t.Fatalf("similar names %v", b["similar"])
	}
	// Unrelated instruction on the same provider: too little history → still price-based.
	if b = budgetOf(t, c, map[string]any{"prompt": "清理 7 天前的日志", "provider": "deepseek"}); b["basis"] != "price" {
		t.Fatalf("unrelated %v", b)
	}
}

func TestRunsPerDay(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for expr, want := range map[string]int{"*/15 * * * *": 96, "0 9 * * 1-5": 1, "0 9,18 * * *": 2, "": 1, "bogus": 1, "* * * * *": 288} {
		if got := runsPerDay(expr, loc); got != want {
			t.Errorf("runsPerDay(%q)=%d want %d", expr, got, want)
		}
	}
	c := login(t)
	b := budgetOf(t, c, map[string]any{"prompt": "检查网站是否可访问", "provider": "deepseek", "cron": "0 */2 * * *"})
	if b["per_day"].(float64) != 12 || b["amount"].(float64) < b["per_run"].(float64)*12-0.011 {
		t.Fatalf("per day %v", b)
	}
	// An inferred schedule counts when the form sends none.
	b = budgetOf(t, c, map[string]any{"prompt": "每 30 分钟检查网站是否可访问", "provider": "deepseek"})
	if b["per_day"].(float64) != 48 {
		t.Fatalf("inferred cron per day %v", b)
	}
}
