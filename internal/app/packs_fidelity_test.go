package app

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// A pack keeps the task's permission level and effort: an exec task must not
// come back as one that cannot run commands, nor lose its effort.
func TestPackKeepsRunModeAndEffort(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	sid := importTestSkill(t, a, "pack-exec-skill")
	tid := packBindingTask(t, a, "Exec task", []tasksvc.SkillBinding{{SkillID: sid, Mode: "follow"}})
	v, err := a.Tasks.Get(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	spec := *v.Spec
	spec.RunMode, spec.Effort = "exec", "high"
	if _, err = a.Tasks.UpdateSpec(ctx, tid, spec, "test", "exec + high"); err != nil {
		t.Fatal(err)
	}
	plain := mkTask(t, a, "Plain task") // "" stays "" (not recorded)
	out := filepath.Join(t.TempDir(), "p.zip")
	if _, err = a.ExportPack(ctx, "modes", []string{tid, plain}, out); err != nil {
		t.Fatal(err)
	}
	b := newTestApp(t)
	res, err := b.ImportPack(ctx, out)
	if err != nil || len(res.TaskIDs) != 2 {
		t.Fatalf("import %+v %v", res, err)
	}
	got, err := b.Tasks.Get(ctx, res.TaskIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.RunMode != "exec" || got.Spec.Effort != "high" || got.Task.Status != "paused" {
		t.Fatalf("run mode/effort lost: mode=%q effort=%q status=%s", got.Spec.RunMode, got.Spec.Effort, got.Task.Status)
	}
	other, err := b.Tasks.Get(ctx, res.TaskIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	if other.Spec.RunMode != "" || other.Spec.Effort != "" {
		t.Fatalf("plain task gained mode/effort: %+v", other.Spec)
	}
}

// A workflow task cannot be carried by a pack; exporting it as a plain prompt
// would import a task that never runs the workflow, so the export says so.
func TestPackExportRefusesWorkflowTask(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tid := mkTask(t, a, "Flow task")
	v, err := a.Tasks.Get(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	spec := *v.Spec
	spec.WorkflowID = "wf_1"
	if _, err = a.Tasks.UpdateSpec(ctx, tid, spec, "test", "run a workflow"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "p.zip")
	if _, err = a.ExportPack(ctx, "flow", []string{tid}, out); err == nil || !strings.Contains(err.Error(), "workflow") {
		t.Fatalf("workflow task exported: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a pack file was written")
	}
}

// Exporting the same tasks twice gives the same skill entries in the same order.
func TestPackExportSkillFilesInStableOrder(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: many\ndescription: d\n---\nx\n"), 0o600)
	for i := 0; i < 20; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("ref-%02d.md", i)), []byte("r"), 0o600)
	}
	sk, err := a.Skills.ImportDir(ctx, "many", dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	tid := packBindingTask(t, a, "Many files", []tasksvc.SkillBinding{{SkillID: sk.ID, Mode: "follow"}})
	var first []string
	for i := 0; i < 5; i++ {
		out := filepath.Join(t.TempDir(), "p.zip")
		if _, err := a.ExportPack(ctx, "many", []string{tid}, out); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.OpenReader(out)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		zr.Close()
		if i == 0 {
			first = names
			if len(names) != 22 || names[0] != "pack.json" || names[1] != "skills/many/SKILL.md" || names[2] != "skills/many/ref-00.md" {
				t.Fatalf("entries %v", names)
			}
			continue
		}
		if strings.Join(names, ",") != strings.Join(first, ",") {
			t.Fatalf("export %d order changed:\n%v\n%v", i, first, names)
		}
	}
}
