package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// The one-time spend page notice about the removed implicit $0.05 caps:
// listed on GET /api/spend, "restore" / "dismiss" behind login and CSRF.
func TestUnlimitedNoticeAPI(t *testing.T) {
	c := login(t)
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": `<b>日报</b>`, "prompt": "p", "provider": "anthropic"})
	id := tk["id"].(string)
	if _, out := c.json(http.MethodGet, "/api/spend", nil); out["unlimited_notice"] != nil {
		t.Fatalf("notice without removed caps: %v", out["unlimited_notice"])
	}
	if _, err := c.s.deps.DB.ExecContext(context.Background(), `INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES('now','task',?,'unlimited_default_migration','system','{"max_amount":0.05,"currency":"USD","action":"pause_notify","warn_at_pct":80}')`, id); err != nil {
		t.Fatal(err)
	}
	_, out := c.json(http.MethodGet, "/api/spend", nil)
	n, _ := out["unlimited_notice"].(map[string]any)
	tasks, _ := n["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["name"] != `<b>日报</b>` {
		t.Fatalf("notice %v", out["unlimited_notice"])
	}
	req := httptest.NewRequest(http.MethodPost, "/api/spend/unlimited-notice", strings.NewReader(`{"action":"restore"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("no csrf: %d", rr.Code)
	}
	if code, _ := c.json(http.MethodGet, "/api/spend/unlimited-notice", nil); code != 405 {
		t.Fatalf("GET %d", code)
	}
	if code, _ := c.json(http.MethodPost, "/api/spend/unlimited-notice", map[string]string{"action": "wipe"}); code != 400 {
		t.Fatalf("bad action %d", code)
	}
	code, res := c.json(http.MethodPost, "/api/spend/unlimited-notice", map[string]string{"action": "restore"})
	if code != 200 || res["restored"] != float64(1) {
		t.Fatalf("restore %d %v", code, res)
	}
	cp, _ := c.s.deps.Tasks.GetCostPolicy(context.Background(), id)
	if cp.RunAmount == nil || *cp.RunAmount != 0.05 {
		t.Fatalf("cap not back %+v", cp)
	}
	if _, out = c.json(http.MethodGet, "/api/spend", nil); out["unlimited_notice"] != nil {
		t.Fatal("notice after restore")
	}
	if code, _ = c.json(http.MethodPost, "/api/spend/unlimited-notice", map[string]string{"action": "restore"}); code != 409 {
		t.Fatalf("second restore %d", code)
	}
}

// New tasks start unlimited and manual; the page code escapes the task names.
func TestUnlimitedFirstFormDefaults(t *testing.T) {
	c := login(t)
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "fresh", "prompt": "p", "provider": "anthropic"})
	cp, err := c.s.deps.Tasks.GetCostPolicy(context.Background(), tk["id"].(string))
	if err != nil || cp.RunAmount != nil || cp.DailyAmount != nil {
		t.Fatalf("new task got a cap %+v %v", cp, err)
	}
	form, _ := os.ReadFile("src/task-form.js")
	src := string(form)
	for _, want := range []string{`smartProvider(st), cron: "",`, `cp = cp || { mode: "global", run_amount: null, daily_amount: daily };`} {
		if !strings.Contains(src, want) {
			t.Errorf("task form: missing %s", want)
		}
	}
	if strings.Contains(src, "0.05") || strings.Contains(src, `"0 9 * * *"`) {
		t.Error("task form still prefills a cap or a schedule")
	}
	spend, _ := os.ReadFile("src/spend.js")
	if !strings.Contains(string(spend), `esc(T("sp.unl_tip", { names:`) || strings.Contains(string(spend), "confirm(") {
		t.Error("spend notice: names must be escaped, no native dialogs")
	}
	wf, _ := os.ReadFile("src/compositions-workflows.js")
	if !strings.Contains(string(wf), "max_parallel:2,timeout_minutes:0,") || !strings.Contains(string(wf), `name="timeout_enabled"`) {
		t.Error("workflow form: timeout must default to unlimited behind a checkbox")
	}
}
