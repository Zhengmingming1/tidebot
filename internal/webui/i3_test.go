package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedRun(t *testing.T, c *client, id, prompt, status string, cost float64, created time.Time) {
	t.Helper()
	_, err := c.s.deps.DB.ExecContext(context.Background(),
		`INSERT INTO runs(id,status,prompt,total_cost_usd,input_tokens,output_tokens,duration_ms,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, status, prompt, cost, int(cost*1000), 1, int(cost*10000), created.UTC().Format(time.RFC3339), created.UTC().Format(time.RFC3339), created.UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
}

func listRuns(t *testing.T, c *client, q string) []map[string]any {
	t.Helper()
	rec := c.do(http.MethodGet, "/api/runs?"+q, "", nil)
	var out []map[string]any
	if rec.Code != 200 {
		t.Fatalf("runs %s: %d %s", q, rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestRunsSearchAndSort(t *testing.T) {
	c := login(t)
	now := time.Now()
	seedRun(t, c, "r1", "summarize hacker news", "succeeded", 0.02, now.Add(-3*time.Hour))
	seedRun(t, c, "r2", "check 100% done_ok", "failed", 0.50, now.Add(-2*time.Hour))
	seedRun(t, c, "r3", "write weekly report", "succeeded", 0.10, now.Add(-1*time.Hour))
	if got := listRuns(t, c, "q=hacker"); len(got) != 1 || got[0]["id"] != "r1" {
		t.Fatalf("q=hacker %v", got)
	}
	// LIKE wildcards are literal
	if got := listRuns(t, c, "q=%25"); len(got) != 1 || got[0]["id"] != "r2" {
		t.Fatalf("q=%% %v", got)
	}
	if got := listRuns(t, c, "q=x'+OR+1=1--"); len(got) != 0 {
		t.Fatalf("injection matched %v", got)
	}
	if got := listRuns(t, c, "sort=cost"); got[0]["id"] != "r2" || got[2]["id"] != "r1" {
		t.Fatalf("sort cost %v", got)
	}
	if got := listRuns(t, c, "sort=old"); got[0]["id"] != "r1" {
		t.Fatalf("sort old %v", got)
	}
	if rec := c.do(http.MethodGet, "/api/runs?sort=bogus", "", nil); rec.Code != 400 {
		t.Fatalf("bad sort %d", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/api/runs?q="+strings.Repeat("a", 101), "", nil); rec.Code != 400 {
		t.Fatalf("long q %d", rec.Code)
	}
}

func TestDashboard(t *testing.T) {
	c := login(t)
	c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "daily", "prompt": "p", "provider": "anthropic", "cron": "*/30 * * * *", "enabled": true})
	seedRun(t, c, "d1", "x", "succeeded", 0.05, time.Now())
	code, out := c.json(http.MethodGet, "/api/dashboard", nil)
	if code != 200 {
		t.Fatalf("dashboard %d", code)
	}
	for _, k := range []string{"running", "next", "tasks", "kpi"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("missing %s in %v", k, out)
		}
	}
	if len(out["next"].([]any)) == 0 {
		t.Fatalf("no upcoming runs %v", out["next"])
	}
	if out["kpi"].(map[string]any)["runs_today"].(float64) < 1 {
		t.Fatalf("kpi %v", out["kpi"])
	}
}

func TestRunEventsResume(t *testing.T) {
	c := login(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "r.log")
	os.WriteFile(p, []byte("one\ntwo\nthree\n[[end]]\n"), 0o600)
	c.s.deps.LogPath = func(string) string { return p }
	seedRun(t, c, "ev1", "x", "succeeded", 0, time.Now())
	rec := c.do(http.MethodGet, "/api/runs/ev1/events", "", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "data: one") || !strings.Contains(body, "id: 8") || !strings.Contains(body, "event: end") {
		t.Fatalf("stream %q", body)
	}
	rec = c.do(http.MethodGet, "/api/runs/ev1/events?from=8", "", nil)
	if b := rec.Body.String(); strings.Contains(b, "data: one") || strings.Contains(b, "data: two") || !strings.Contains(b, "data: three") {
		t.Fatalf("resume %q", b)
	}
}

func TestRerunNeedsCSRFAndUnknown(t *testing.T) {
	c := login(t)
	if code, _ := c.json(http.MethodPost, "/api/runs/nope/rerun", nil); code != 404 {
		t.Fatalf("unknown rerun %d", code)
	}
}
