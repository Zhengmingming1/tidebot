package webui

import (
	"encoding/json"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"strings"
	"testing"
)

func TestTaskTimeoutFormExplicitZeroAndLegacyOmission(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{{`{}`, 17}, {`{"timeout_minutes":0}`, 0}, {`{"timeout_minutes":90}`, 90}} {
		var f taskForm
		if err := json.Unmarshal([]byte(tc.body), &f); err != nil {
			t.Fatal(err)
		}
		got := specFromForm(tasksvc.Spec{TimeoutMinutes: 17}, f)
		if got.TimeoutMinutes != tc.want {
			t.Fatalf("%s: %d", tc.body, got.TimeoutMinutes)
		}
	}
}

func TestTaskTimeoutToggleMarkup(t *testing.T) {
	b, err := Static.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`timeout_minutes: 0 };`, `name="timeout_enabled"`, `f.timeout_minutes.disabled = !enabled;`, `f.timeout_minutes.required = enabled;`, `timeout_minutes: f.timeout_enabled.checked ? Number(f.timeout_minutes.value) : 0`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing timeout control: %s", want)
		}
	}
	if strings.Contains(string(b), `timeout_minutes: parseInt(f.timeout_minutes.value, 10) || 30`) {
		t.Fatal("hidden timeout default remains")
	}
}

func TestUnlimitedTaskExportIsExplicit(t *testing.T) {
	raw, err := json.Marshal(taskFileEntry{TimeoutMinutes: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"timeout_minutes":0`) {
		t.Fatalf("unlimited timeout omitted: %s", raw)
	}
}
