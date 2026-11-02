package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func TestCombinedRunStagesPrimaryAndAuxiliary(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ctx := context.Background()
	main, helper := importTestSkill(t, a, "primary"), importTestSkill(t, a, "helper")
	bindings := []tasksvc.SkillBinding{{SkillID: helper, Role: "auxiliary"}, {SkillID: main, Role: "primary"}}
	st, err := a.stageSkills(ctx, bindings)
	if err != nil {
		t.Fatal(err)
	}
	args, err := st.CheckedArgs(true, true)
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(argAfter(args, "--append-system-prompt-file"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "Auxiliary skill: helper") || !strings.Contains(string(text), st.Skills[1].Dir) {
		t.Fatalf("missing helper instructions: %s", text)
	}
	if p := st.Invoke(true, "do work"); p != "/tidebot:primary do work" {
		t.Fatal(p)
	}
	st.Close()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	runID, err := a.startRun(webui.RunRequest{Prompt: "do work", SkillBindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	got, err := a.Skills.RunBindings(ctx, runID)
	if err != nil || len(got) != 2 || got[0].SkillID != main || got[1].SkillID != helper || got[0].VersionID == "" {
		t.Fatalf("snapshot: %v %v", got, err)
	}
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "--disallowedTools") || strings.Contains(string(raw), "/tidebot:primary /tidebot:helper") {
		t.Fatalf("arguments %s", raw)
	}
}

func TestCompositionFailureCannotRunWithoutSkills(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "broken composition", Prompt: "do work"})
	if err != nil {
		t.Fatal(err)
	}
	sp := *tv.Spec
	sp.SkillBindings = []tasksvc.SkillBinding{{SkillID: "missing"}}
	if _, err = a.Tasks.UpdateSpec(ctx, tv.Task.ID, sp, "test", "test"); err != nil {
		t.Fatal(err)
	}
	if err = a.runTaskTrigger(ctx, tv.Task, "manual", ""); err == nil {
		t.Fatal("ran without required skills")
	}
	var n int
	_ = a.DB.QueryRow(`SELECT count(*) FROM runs WHERE task_id=?`, tv.Task.ID).Scan(&n)
	if n != 0 {
		t.Fatal("invalid composition launched a run")
	}
}

func TestLegacyCompositionNeverTruncates(t *testing.T) {
	a := newTestApp(t)
	main, helper := importTestSkill(t, a, "primary"), importTestSkill(t, a, "helper")
	st, err := a.stageSkills(context.Background(), []tasksvc.SkillBinding{{SkillID: main}, {SkillID: helper}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Skills[1].SkillMD = []byte(strings.Repeat("long instructions\n", 2048))
	if _, err = st.CheckedArgs(false, true); err == nil {
		t.Fatal("silently truncated a composition")
	}
}
