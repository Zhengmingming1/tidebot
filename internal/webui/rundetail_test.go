package webui

import (
	"os"
	"strings"
	"testing"
)

// TestRunDetailActions: a finished run can be rerun from its page (then the new run opens),
// and a quick run can be saved as a task with its prompt and provider prefilled.
func TestRunDetailActions(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		`id="rd-rerun"' + (live ? " disabled" : "")`,
		`api("/api/runs/" + enc(id) + "/rerun", { method: "POST", body: {} })`,
		`(r.task_id ? "" : '<button type="button" class="btn sm" id="rd-save-task"`,
		`taskFormModal(null, { prompt: r.prompt, provider: r.provider, skill_id: r.skill_id, workspace: r.workspace })`,
		`prompt: pre.prompt || ""`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
}
