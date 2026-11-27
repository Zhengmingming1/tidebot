package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// tasks.create (agentctl tasks create) builds the same v1 the WebUI form does: a
// permission level (exec by default, never the legacy ""), effort and skills.
func TestRPCTasksCreateFullSpec(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	sk := importSkill(t, a, "weekly-notes", "Summarize the week.")
	var cur string
	if err := a.DB.QueryRow(`SELECT version_id FROM pointers WHERE entity_type='skill' AND entity_id=? AND name='current'`, sk).Scan(&cur); err != nil {
		t.Fatal(err)
	}
	create := func(p map[string]any) (rpcTaskCreated, error) {
		raw, _ := json.Marshal(p)
		out, err := a.rpcTasksCreate(ctx, raw)
		if err != nil {
			return rpcTaskCreated{}, err
		}
		return out.(rpcTaskCreated), nil
	}
	spec := func(id string) tasksvc.Spec {
		v, err := a.Tasks.Get(ctx, id)
		if err != nil || v.Spec == nil {
			t.Fatal(err)
		}
		return *v.Spec
	}

	// basic fields only: exec, no effort, no skills, a single version
	got, err := create(map[string]any{"name": " Daily ", "prompt": " do it ", "provider_name": "kimi-coding"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Name != "Daily" || got.Task.Status != "active" || got.Task.CronExpr != "" {
		t.Fatalf("task = %+v", got.Task)
	}
	if sp := spec(got.Task.ID); sp.RunMode != "exec" || sp.Effort != "" || len(sp.SkillBindings) != 0 || sp.Prompt != "do it" {
		t.Fatalf("default spec = %+v", sp)
	}
	vs, _ := a.Ver.ListVersions(ctx, version.EntityTask, got.Task.ID)
	if len(vs) != 1 {
		t.Fatalf("versions = %d, want 1 (everything in v1)", len(vs))
	}
	// JSON answer keeps the old shape (task at the top) for scripts
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"task":{`) {
		t.Fatalf("answer shape: %s", raw)
	}

	// level, effort and a followed skill
	got, err = create(map[string]any{"name": "Notes", "prompt": "p", "run_mode": "read", "effort": "high", "skill_id": sk})
	if err != nil {
		t.Fatal(err)
	}
	sp := spec(got.Task.ID)
	if sp.RunMode != "read" || sp.Effort != "high" || len(sp.SkillBindings) != 1 || sp.SkillBindings[0].SkillID != sk || sp.SkillBindings[0].Mode != "follow" || sp.SkillBindings[0].Role != "primary" {
		t.Fatalf("spec = %+v", sp)
	}
	if got.Warning != "" {
		t.Fatalf("read task warned: %q", got.Warning)
	}
	// pinned version
	got, err = create(map[string]any{"name": "Pinned", "prompt": "p", "run_mode": "edit", "skill_id": sk, "skill_version_id": cur})
	if err != nil {
		t.Fatal(err)
	}
	if b := spec(got.Task.ID).SkillBindings; len(b) != 1 || b[0].Mode != "pinned" || b[0].VersionID != cur {
		t.Fatalf("pinned = %+v", b)
	}

	// refused before anything is written
	count := func() (n int) {
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n)
		return n
	}
	before := count()
	for name, p := range map[string]map[string]any{
		"empty mode":      {"name": "x", "prompt": "p", "run_mode": ""},
		"bad mode":        {"name": "x", "prompt": "p", "run_mode": "root"},
		"bad effort":      {"name": "x", "prompt": "p", "effort": "max"},
		"unknown skill":   {"name": "x", "prompt": "p", "skill_id": "sk_missing"},
		"unknown version": {"name": "x", "prompt": "p", "skill_id": sk, "skill_version_id": "ver_missing"},
		"version alone":   {"name": "x", "prompt": "p", "skill_version_id": cur},
		"provider":        {"name": "x", "prompt": "p", "provider_name": "no-such-provider"},
		"long name":       {"name": strings.Repeat("名", 81), "prompt": "p"},
		"no prompt":       {"name": "x", "prompt": "  "},
		"workflow+skill":  {"name": "x", "workflow_id": "wf_1", "skill_id": sk},
	} {
		if _, err := create(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// internal-only fields can't be set over RPC
	if _, err := create(map[string]any{"name": "y", "prompt": "p", "InitialStatus": "paused", "PauseKind": "pack"}); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != before+1 {
		t.Fatalf("tasks %d → %d, want one more", before, n)
	}
	var st, pk string
	if err := a.DB.QueryRow(`SELECT status, COALESCE(pause_kind,'') FROM tasks WHERE name='y'`).Scan(&st, &pk); err != nil || st != "active" || pk != "" {
		t.Fatalf("internal fields leaked in: %q %q %v", st, pk, err)
	}
}

func TestExecSandboxWarning(t *testing.T) {
	if w := execSandboxWarning(guard.SandboxStatus{Status: guard.SandboxOK}); w != "" {
		t.Fatalf("ok warned: %q", w)
	}
	if w := execSandboxWarning(guard.SandboxStatus{Status: guard.SandboxNone}); w != "" {
		t.Fatalf("none warned: %q", w)
	}
	w := execSandboxWarning(guard.SandboxStatus{Status: guard.SandboxMissing, Missing: []string{"bubblewrap"}, Install: "apt install bubblewrap"})
	if !strings.Contains(w, "bubblewrap") || !strings.Contains(w, "--mode") {
		t.Fatalf("missing = %q", w)
	}
	if w := execSandboxWarning(guard.SandboxStatus{Status: guard.SandboxBroken}); !strings.Contains(w, "--mode") {
		t.Fatalf("broken = %q", w)
	}
}
