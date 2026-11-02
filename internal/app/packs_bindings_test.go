package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func packBindingTask(t *testing.T, a *App, name string, bindings []tasksvc.SkillBinding) string {
	t.Helper()
	ctx := context.Background()
	v, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: name, Prompt: "Run the selected skills"})
	if err != nil {
		t.Fatal(err)
	}
	spec := *v.Spec
	spec.SkillBindings = bindings
	if _, err = a.Tasks.UpdateSpec(ctx, v.Task.ID, spec, "test", "bind pack fixture"); err != nil {
		t.Fatal(err)
	}
	return v.Task.ID
}

func TestPackRejectsDifferentVersionsOfOneSkill(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	sid := importTestSkill(t, a, "shared-pack-skill")
	first, err := a.Skills.ResolveVersion(ctx, tasksvc.SkillBinding{SkillID: sid, Mode: "follow"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.Skills.Files(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	files["SKILL.md"] = append(files["SKILL.md"], []byte("\nVersion two only\n")...)
	second, _, err := a.Skills.CommitFiles(ctx, sid, "shared-pack-skill", files, "test", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	oldTask := packBindingTask(t, a, "Pinned old", []tasksvc.SkillBinding{{SkillID: sid, Mode: "pinned", VersionID: first}})
	newTask := packBindingTask(t, a, "Follow new", []tasksvc.SkillBinding{{SkillID: sid, Mode: "follow"}})
	out := filepath.Join(t.TempDir(), "keep.zip")
	if err = os.WriteFile(out, []byte("existing export"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tasks := range [][]string{{oldTask, newTask}, {newTask, oldTask}} {
		if _, err = a.ExportPack(ctx, "conflicting versions", tasks, out); err == nil || !strings.Contains(err.Error(), "different versions") || !strings.Contains(err.Error(), "separately") {
			t.Fatalf("expected actionable version conflict, got %v", err)
		}
		b, err := os.ReadFile(out)
		if err != nil || string(b) != "existing export" {
			t.Fatal("failed export overwrote destination")
		}
	}
	// Sharing an identical pinned version is still valid even when current moved.
	otherOld := packBindingTask(t, a, "Also old", []tasksvc.SkillBinding{{SkillID: sid, Mode: "pinned", VersionID: first}})
	if _, err = a.ExportPack(ctx, "same versions", []string{oldTask, otherOld}, out); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name == "skills/shared-pack-skill/SKILL.md" {
			found = true
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(r)
			r.Close()
			if err != nil || strings.Contains(string(b), "Version two only") {
				t.Fatalf("wrong version exported: %s %v", b, err)
			}
		}
	}
	if !found {
		t.Fatal("resolved skill was omitted from export")
	}
	if second.ID == first {
		t.Fatal("fixture did not create a second version")
	}
}

func TestPackPreservesCompositionPrimaryOrder(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	primary, aux := importTestSkill(t, a, "pack-primary"), importTestSkill(t, a, "pack-aux")
	tid := packBindingTask(t, a, "Composed task", []tasksvc.SkillBinding{{SkillID: aux, Mode: "follow", Role: "auxiliary"}, {SkillID: primary, Mode: "follow", Role: "primary"}})
	out := filepath.Join(t.TempDir(), "composition.zip")
	if _, err := a.ExportPack(ctx, "composition", []string{tid}, out); err != nil {
		t.Fatal(err)
	}
	b := newTestApp(t)
	result, err := b.ImportPack(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.Tasks.Get(ctx, result.TaskIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	bindings := v.Spec.SkillBindings
	if len(bindings) != 2 || bindings[0].SkillName != "pack-primary" || bindings[0].Role != "primary" || bindings[1].SkillName != "pack-aux" || bindings[1].Role != "auxiliary" || v.Task.Status != "paused" {
		t.Fatalf("composition changed: %+v", v)
	}
}

func TestPackMissingSkillRejectedBeforeImport(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		files                map[string]string
		taskSkills, declared []string
	}{
		{"missing auxiliary", map[string]string{"skills/primary/SKILL.md": "# Main"}, []string{"primary", "missing-aux"}, []string{"primary"}},
		{"declared but absent", map[string]string{"skills/primary/SKILL.md": "# Main"}, []string{"primary"}, []string{"primary", "missing"}},
		{"missing SKILL.md", map[string]string{"skills/primary/SKILL.md": "# Main", "skills/aux/helper.py": "pass"}, []string{"primary", "aux"}, []string{"primary", "aux"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			p := Pack{Format: PackFormat, Name: "broken", Tasks: []PackTask{{Name: "Broken task", Prompt: "Run composed task", Skills: tc.taskSkills}}, Skills: tc.declared}
			raw, _ := json.Marshal(p)
			w, err := zw.Create("pack.json")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(raw); err != nil {
				t.Fatal(err)
			}
			for name, content := range tc.files {
				w, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.Write([]byte(content)); err != nil {
					t.Fatal(err)
				}
			}
			if err = zw.Close(); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "broken.zip")
			if err = os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err = a.ImportPack(ctx, file); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("missing skill was ignored: %v", err)
			}
			if tasks, err := a.Tasks.List(ctx); err != nil || len(tasks) != 0 {
				t.Fatalf("created partial tasks: %v %v", tasks, err)
			}
			if skills, err := a.Skills.List(ctx); err != nil || len(skills) != 0 {
				t.Fatalf("imported partial skill set: %v %v", skills, err)
			}
		})
	}
}

func TestPackExportMissingBindingDoesNotDropSkill(t *testing.T) {
	a := newTestApp(t)
	tid := packBindingTask(t, a, "Broken reference", []tasksvc.SkillBinding{{SkillID: "missing_skill", Mode: "follow"}})
	out := filepath.Join(t.TempDir(), "missing.zip")
	if _, err := a.ExportPack(context.Background(), "broken", []string{tid}, out); err == nil {
		t.Fatal("missing binding silently omitted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("invalid export created a file: %v", err)
	}
}
