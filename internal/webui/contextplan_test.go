package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
)

func TestContextPlanRunDetailRequiresAuthentication(t *testing.T) {
	c := login(t)
	seedRun(t, c, "context-run", "private prompt", "succeeded", 0, time.Now())
	plan := contextplan.Plan{ID: "plan-one", Policy: contextplan.PolicyVersion, EstimatedTokens: 25, BudgetTokens: 61440}
	raw, _ := json.Marshal(plan)
	if _, err := c.s.deps.DB.Exec(`INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES('now','run','context-run','context.plan','test',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	code, out := c.json(http.MethodGet, "/api/runs/context-run", nil)
	if code != 200 || out["context_plan"].(map[string]any)["id"] != "plan-one" {
		t.Fatal(code, out)
	}
	rr := httptest.NewRecorder()
	c.s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/runs/context-run", nil))
	if rr.Code != 401 {
		t.Fatal("unauthenticated plan access", rr.Code)
	}
}
