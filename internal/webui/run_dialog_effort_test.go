package webui

import (
	"net/http"
	"strings"
	"testing"
)

// The Run dialog fills in the effort of the picked skill's last quick run (folder or
// not); other skills, other triggers and runs that let Claude Code decide don't leak in.
func TestRunDefaultsSkillEffort(t *testing.T) {
	c := login(t)
	db := c.s.deps.DB
	ins := func(id, skill, trig, effort, at string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO runs(id,status,prompt,trigger_kind,skill_id,effort,created_at) VALUES(?,?,?,?,?,?,?)`,
			id, "succeeded", "p", trig, skill, effort, at); err != nil {
			t.Fatal(err)
		}
	}
	ins("e1", "sk-a", "quick", "low", "2026-11-20T01:00:00Z")
	ins("e2", "sk-a", "quick", "high", "2026-11-21T01:00:00Z") // newest quick run of sk-a, no folder
	ins("e3", "sk-a", "schedule", "medium", "2026-11-22T01:00:00Z")
	ins("e4", "sk-b", "quick", "", "2026-11-22T02:00:00Z")
	ins("e5", "sk-c", "quick", "max", "2026-11-22T03:00:00Z") // not a valid value
	for skill, want := range map[string]string{"sk-a": "high", "sk-b": "", "sk-c": "", "sk-none": ""} {
		code, out := c.json(http.MethodGet, "/api/runs/defaults?skill_id="+skill, nil)
		if code != 200 {
			t.Fatalf("%s: %d", skill, code)
		}
		if e, ok := out["effort"].(string); !ok || e != want {
			t.Errorf("%s: effort %v, want %q", skill, out["effort"], want)
		}
	}
	// without a skill there is nothing to infer
	if code, out := c.json(http.MethodGet, "/api/runs/defaults", nil); code != 200 || out["effort"] != nil {
		t.Fatalf("no skill: %d %v", code, out["effort"])
	}
	if p := planOf(t, c, `SELECT COALESCE(effort,'') FROM runs WHERE skill_id=? AND trigger_kind='quick' ORDER BY created_at DESC LIMIT 1`, "sk-a"); strings.Contains(p, "SCAN runs") && !strings.Contains(p, "INDEX") {
		t.Fatalf("full scan: %s", p)
	}
}

// The dialog has an effort picker: it starts at the rerun's effort, is sent as-is,
// counts as an edit, is reverted by "Revert", and a skill's last effort only fills a
// picker you haven't touched.
func TestRunDialogEffortPicker(t *testing.T) {
	js := appJSSource(t)
	for _, want := range []string{
		`<select name="effort">' + effortOptions(pre.effort) + "</select></label></div>"`,
		`effTouched = !!edit`,
		`mode: sentMode(), effort: form.effort.value }; };`,
		`(c.effort || "") !== (orig.effort || "")`,
		`form.effort.value = orig.effort;`,
		`if (fill && sk && !effTouched) { var e = (d && d.effort) || "";`,
		`form.effort.addEventListener("change", function () { effTouched = true; sync(); });`,
		`function effortOptions(cur)`,
		`<select name="effort">' + effortOptions(tf.effort) + "</select></label></div>"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	if strings.Contains(js, `effort: (edit && edit.effort) || ""`) {
		t.Error("the dialog still submits the source run's effort instead of the picker")
	}
}
