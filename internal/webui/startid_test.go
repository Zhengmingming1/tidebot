package webui

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Start requests answer with the new run's id, so the UI opens exactly that run instead of
// guessing "the newest run" after a delay. A queued run (no id yet) answers without one, and
// a run that could not start (budget blocked) is an error, not a silent 202.
func TestStartAnswersWithRunID(t *testing.T) {
	c := login(t)
	var gotPrompt, gotProv string
	c.s.deps.QuickRun = func(prompt, prov string) (string, error) {
		gotPrompt, gotProv = prompt, prov
		switch prompt {
		case "blocked":
			return "", errors.New("budget blocked: daily cap")
		case "queued":
			return "", nil
		}
		return "run_new1", nil
	}
	c.s.deps.RunTask = func(taskID, bucket string) (string, error) { return "run_task1", nil }

	code, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "hi", "provider": "kimi"})
	if code != 202 || out["run_id"] != "run_new1" || gotProv != "kimi" {
		t.Fatalf("quick %d %v", code, out)
	}
	if code, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "queued"}); code != 202 || out["run_id"] != nil || out["ok"] != true {
		t.Fatalf("queued %d %v", code, out)
	}
	if code, out := c.json(http.MethodPost, "/api/runs/quick", map[string]any{"prompt": "blocked"}); code != 500 || !strings.Contains(out["error"].(string), "budget") {
		t.Fatalf("blocked %d %v", code, out)
	}

	seedRun(t, c, "run_old", "again please", "succeeded", 0.01, time.Now())
	if code, out := c.json(http.MethodPost, "/api/runs/run_old/rerun", nil); code != 202 || out["run_id"] != "run_new1" || gotPrompt != "again please" {
		t.Fatalf("rerun %d %v", code, out)
	}
}

func TestStartOpensExactRun(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function openStarted(res, msg) {`,
		`location.hash = "#/runs/" + enc(res.run_id)`,
		`close(); openStarted(r, "toast.quick_started");`,
		`openStarted(res, "runs.rerun_started");`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	// No more "open the newest run after a pause": with several runs going it opened the wrong one.
	if strings.Contains(src, `api("/api/runs?limit=1")`) {
		t.Error("app.js still guesses the new run as the newest one")
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		b, _ := os.ReadFile(f)
		if !strings.Contains(string(b), `"toast.run_queued"`) {
			t.Errorf("%s lacks toast.run_queued", f)
		}
	}
}
