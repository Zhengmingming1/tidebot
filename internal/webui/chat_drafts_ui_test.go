package webui

import (
	"strings"
	"testing"
)

// The draft bar adopts only after the user confirms, and the task form only
// suggests a schedule read from the prompt instead of filling it in.
func TestChatDraftUIMarkup(t *testing.T) {
	b, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{`function csDraftBar(`, `id="cs-tune"`, `id="cs-save-task"`, `"/task-preset"`, `"/task-preset?from=run"`, `api(base + "/adopt"`, `suggest(g.cron);`, `id="af-sched"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(js, `se.set(g.cron)`) {
		t.Fatal("prompt schedule is still filled in automatically")
	}
	i := strings.Index(js, `api(base + "/adopt"`)
	if j := strings.LastIndex(js[:i], "confirmDialog("); j < 0 || i-j > 800 {
		t.Fatal("adopt is not behind a confirm dialog")
	}
}
