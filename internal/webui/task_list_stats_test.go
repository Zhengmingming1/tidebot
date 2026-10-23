package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The task list carries each task's last 10 runs (oldest first), the newest run's status and
// time, and runs today counted from local midnight in the display zone.
func TestTaskListRecentRuns(t *testing.T) {
	c := login(t)
	c.s.deps.Location = time.FixedZone("UTC+14", 14*3600)
	mk := func(name string) string {
		code, out := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": name, "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
		if code >= 300 {
			t.Fatalf("create %s: %d %v", name, code, out)
		}
		return fmt.Sprint(out["id"])
	}
	busy, idle, gone := mk("busy"), mk("idle"), mk("gone")
	now := time.Now().In(c.s.loc())
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, c.s.loc())
	// 12 runs: the two oldest fall outside the strip; #10 is yesterday 23:59 local, #11 today 00:01.
	sts := []string{"failed", "failed", "succeeded", "timeout", "succeeded", "canceled", "skipped", "succeeded", "succeeded", "succeeded", "failed", "running"}
	for i, st := range sts {
		at := midnight.Add(time.Duration(i-12) * time.Hour)
		switch i {
		case 10:
			at = midnight.Add(-time.Minute)
		case 11:
			at = midnight.Add(time.Minute)
		}
		id := fmt.Sprintf("b%02d", i)
		seedRun(t, c, id, "p", st, 0.01, at)
		if _, err := c.s.deps.DB.Exec(`UPDATE runs SET task_id=? WHERE id=?`, busy, id); err != nil {
			t.Fatal(err)
		}
	}
	seedRun(t, c, "g0", "p", "succeeded", 0.01, now)
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET task_id=? WHERE id='g0'`, gone); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.json(http.MethodDelete, "/api/tasks/"+gone, nil); code >= 300 {
		t.Fatalf("delete %d", code)
	}

	rec := c.do(http.MethodGet, "/api/tasks", "", nil)
	if rec.Code != 200 {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	var list []struct {
		ID         string `json:"id"`
		LastStatus string `json:"last_status"`
		LastRunAt  string `json:"last_run_at"`
		RunsToday  int    `json:"runs_today"`
		Beats      []beat `json:"beats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("archived task listed: %+v", list)
	}
	for _, tk := range list {
		switch tk.ID {
		case busy:
			if len(tk.Beats) != taskListBeats {
				t.Fatalf("beats = %d, want %d", len(tk.Beats), taskListBeats)
			}
			if tk.Beats[0].ID != "b02" || tk.Beats[9].ID != "b11" {
				t.Fatalf("beats not the newest 10 oldest first: %s … %s", tk.Beats[0].ID, tk.Beats[9].ID)
			}
			if tk.LastStatus != "running" || tk.LastRunAt != midnight.Add(time.Minute).UTC().Format(time.RFC3339) {
				t.Fatalf("last = %s at %s", tk.LastStatus, tk.LastRunAt)
			}
			if tk.Beats[0].CostUSD <= 0 || tk.Beats[0].DurationMS <= 0 || tk.Beats[0].At == "" {
				t.Fatalf("beat fields missing: %+v", tk.Beats[0])
			}
			if tk.RunsToday != 1 {
				t.Fatalf("runs today = %d, want 1 (local day, not the UTC date)", tk.RunsToday)
			}
		case idle:
			if len(tk.Beats) != 0 || tk.LastStatus != "" || tk.LastRunAt != "" || tk.RunsToday != 0 {
				t.Fatalf("idle task has runs: %+v", tk)
			}
		default:
			t.Fatalf("unexpected task %s", tk.ID)
		}
	}
	if strings.Contains(rec.Body.String(), `"beats":null`) {
		t.Fatal("beats should be omitted, not null")
	}
	// the detail answer stays as it was
	if rec := c.do(http.MethodGet, "/api/tasks/"+busy, "", nil); strings.Contains(rec.Body.String(), `"beats"`) {
		t.Fatal("detail answer carries beats")
	}
}

func TestTaskListRecentRunsUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function taskBeatsCell(t) {`,
		`return /^(succeeded|failed|timeout)$/.test(b.status);`,
		`beatsBar(bs, "sm")`,
		`agoHtml(t.last_run_at || last.at)`,
		`<td class=\"tl-last\">" + taskBeatsCell(t)`,
		`<td class="tl-name"><div class="cell-main">`,
		`last: function (t) { return t.last_run_at || ""; }`,
		`th("last", T("col.recent_runs"))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	css, _ := os.ReadFile("static/styles.css")
	for _, want := range []string{".hbar.sm .hb { width: 6px; height: 16px;", ".table td.tl-name { width: 100%; max-width: 0; }", "html.tb-hidden .hbar.sm .hb.run { animation-play-state: paused; }", "@media (prefers-reduced-motion: reduce) { .hbar.sm .hb.run { animation: none; } }"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		raw, _ := os.ReadFile(f)
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"col.recent_runs", "tl.beats_aria", "tl.ok_of", "tl.ok_tip", "tasks.never"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
