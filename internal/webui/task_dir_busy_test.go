package webui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// A task run refused because another run is changing its folder answers like
// the Run dialog (409, problem "busy"), from "Run now" and from a task run's
// "Run again", so the page can say why in the reader's language; a refused
// folder keeps its problem code.
func TestTaskRunFolderBusyAnswer(t *testing.T) {
	c := login(t)
	var answer error
	c.s.deps.RunTask = func(taskID, bucket string) (string, error) { return "", answer }

	answer = fmt.Errorf("workflow: %w", guard.ErrDirBusy)
	code, out := c.json(http.MethodPost, "/api/tasks/task_x/run", map[string]any{})
	if code != 409 || out["problem"] != "busy" {
		t.Fatalf("run now %d %v", code, out)
	}
	tv, err := c.s.deps.Tasks.Create(context.Background(), tasksvc.CreateInput{Name: "in a folder", Prompt: "work", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	seedRun(t, c, "run_t1", "again", "succeeded", 0.01, time.Now())
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET task_id=? WHERE id='run_t1'`, tv.Task.ID); err != nil {
		t.Fatal(err)
	}
	if code, out := c.json(http.MethodPost, "/api/runs/run_t1/rerun", nil); code != 409 || out["problem"] != "busy" {
		t.Fatalf("rerun %d %v", code, out)
	}
	answer = &guard.DirError{Problem: guard.DirNotFound}
	if code, out := c.json(http.MethodPost, "/api/runs/run_t1/rerun", nil); code != 400 || out["problem"] != guard.DirNotFound {
		t.Fatalf("rerun missing folder %d %v", code, out)
	}
	answer = fmt.Errorf("budget blocked")
	if code, out := c.json(http.MethodPost, "/api/tasks/task_x/run", map[string]any{}); code != 500 || out["problem"] != nil {
		t.Fatalf("other error %d %v", code, out)
	}

	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, want := range []string{
		`function startFail(e) {`,
		`toast(T("rn.ws_" + pr + "_tip"), true)`,
		`toast(startedText(res, "toast.run_started")); if (/^#\/tasks$/.test(location.hash)) refresh(); }).catch(startFail)`,
		`toast(startedText(res, "toast.run_started")); reload(); }).catch(startFail)`,
		`b.disabled = false; startFail(err);`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, f := range []string{"static/i18n/zh-CN.json", "static/i18n/en.json"} {
		b, _ := os.ReadFile(f)
		if !strings.Contains(string(b), `"rn.ws_busy_tip"`) || !strings.Contains(string(b), `"rn.ws_not_found_tip"`) {
			t.Errorf("%s lacks the folder problem texts", f)
		}
	}
}
