package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/optimize"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func TestCanaryRoutingInRuns(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	d := t.TempDir()
	md := "---\nname: s\ndescription: d\n---\n\nstep one\nstep two\nstep three\nstep four\n"
	_ = os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644)
	sk, err := a.Skills.ImportDir(ctx, "s", d, "user")
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Optimize.SetSettings(ctx, sk.ID, optimize.Settings{Mode: "canary", Ratio: 1, Runs: 5})
	p, err := a.Optimize.Propose(ctx, sk.ID, map[string][]byte{"SKILL.md": []byte(md + "step five\n")}, "auto", "logic", "")
	if err != nil || p.Status != "canary" {
		t.Fatal(err, p)
	}
	task, _ := a.EnsureTask(ctx, "t", "p", "", "", "")
	b := []tasksvc.SkillBinding{{SkillID: sk.ID, Mode: "follow"}}
	out, ch := a.chooseSkillVersions(ctx, task.ID, "production", b)
	if out[0].VersionID != p.VersionID || !ch[0].Candidate {
		t.Fatal("candidate expected")
	}
	_ = a.Policy.Put(ctx, policy.Policy{TaskID: task.ID, SideEffects: true})
	if _, ch := a.chooseSkillVersions(ctx, task.ID, "production", b); ch[0].Candidate {
		t.Fatal("side effects never canary")
	}
	a.recordSkillOutcomes(ctx, []skillChoice{{sk.ID, p.VersionID, true}}, runner.Result{RunID: "r1"}, os.ErrClosed, false)
	a.recordSkillOutcomes(ctx, []skillChoice{{sk.ID, p.VersionID, true}}, runner.Result{RunID: "r2"}, os.ErrClosed, false)
	if l, _ := a.Optimize.Proposals(ctx, sk.ID); l[0].Status != "withdrawn" {
		t.Fatal(l[0].Status)
	}
}

func TestSkillOptimizerReachableFromWebUI(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: w\ndescription: d\n---\nx\n"), 0o644)
	sk, err := a.Skills.ImportDir(ctx, "w", d, "user")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"skill_id": sk.ID, "mode": "propose"})
	out, err := a.CallExt(ctx, "skills.optimize_settings", raw)
	if err != nil || out.(optimize.Settings).Mode != "propose" {
		t.Fatal(err, out)
	}
	for _, m := range []string{"skills.proposals", "skills.promote", "skills.discard", "skills.compare", "skills.canary_start"} {
		if _, ok := a.extRPC()[m]; !ok {
			t.Fatalf("%s not exposed", m)
		}
	}
}
