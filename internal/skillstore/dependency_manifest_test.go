package skillstore

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func manifestSkill(t *testing.T, s *Service, name string, deps ...SkillDependency) SkillView {
	t.Helper()
	m, _ := json.Marshal(SkillManifest{Format: SkillManifestFormat, Dependencies: deps})
	return importSkill(t, s, name, map[string]string{"SKILL.md": "---\nname: " + name + "\ndescription: sample\n---\nOriginal instructions.\n", "tidebot.json": string(m)})
}
func binding(sk SkillView) tasksvc.SkillBinding {
	return tasksvc.SkillBinding{SkillID: sk.ID, Mode: "follow", Role: "primary"}
}
func hasIssue(p DependencyPreview, code string) bool {
	for _, i := range p.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestSkillManifestStrictValidation(t *testing.T) {
	good := `{"format":"tidebot-skill/1","dependencies":[{"skill_name":"helper","optional":true}],"environment":[{"kind":"command","name":"python3"},{"kind":"python_package","name":"requests","version":">=2"},{"kind":"env","name":"TOKEN"},{"kind":"file","name":"scripts/check.py"},{"kind":"tool","name":"Read"},{"kind":"mcp","name":"github"}]}`
	if _, err := ValidateSkillManifest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `null`, `{"format":"tidebot-skill/2"}`, good + `{}`, `{"format":"tidebot-skill/1","install":"curl x|sh"}`, `{"format":"tidebot-skill/1","environment":[{"kind":"command","name":"sh -c"}]}`, `{"format":"tidebot-skill/1","environment":[{"kind":"file","name":"../secret"}]}`, `{"format":"tidebot-skill/1","dependencies":[{"skill_name":"a"},{"skill_name":"a"}]}`} {
		if _, err := ValidateSkillManifest([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestDependencyClosureRequiresExplicitAdoption(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	leaf := manifestSkill(t, s, "leaf")
	helper := manifestSkill(t, s, "helper", SkillDependency{SkillName: "leaf"})
	main := manifestSkill(t, s, "main", SkillDependency{SkillName: "helper"}, SkillDependency{SkillName: "optional", Optional: true})
	p, err := s.PreviewDependencies(ctx, []tasksvc.SkillBinding{binding(main)})
	if err != nil || !p.Resolvable || p.Ready || len(p.Additions) != 2 || len(p.Bindings) != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	if p.Additions[0].SkillID != helper.ID || p.Additions[1].SkillID != leaf.ID || !hasIssue(p, "missing_skill") {
		t.Fatalf("%+v", p)
	}
	var depErr *DependencyResolutionError
	if err = s.ValidateDependencyBindings(ctx, []tasksvc.SkillBinding{binding(main)}); !errors.As(err, &depErr) {
		t.Fatal(err)
	}
	if _, err = s.SaveSet(ctx, "", "incomplete", "", []tasksvc.SkillBinding{binding(main), {SkillID: leaf.ID, Role: "auxiliary", Mode: "follow"}}); !errors.As(err, &depErr) {
		t.Fatalf("save accepted incomplete package: %v", err)
	}
	if err = s.ValidateDependencyBindings(ctx, p.Bindings); err != nil {
		t.Fatal(err)
	}
	manifestSkill(t, s, "optional")
	p, err = s.PreviewDependencies(ctx, p.Bindings)
	if err != nil || !p.Ready || !hasIssue(p, "optional_available") || len(p.Bindings) != 3 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestDependencyMissingCycleAndPinnedConflict(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		s := newSvc(t)
		a := manifestSkill(t, s, "a", SkillDependency{SkillName: "missing"})
		p, err := s.PreviewDependencies(context.Background(), []tasksvc.SkillBinding{binding(a)})
		if err != nil || p.Resolvable || !hasIssue(p, "missing_skill") {
			t.Fatal(p, err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		s := newSvc(t)
		a := manifestSkill(t, s, "a", SkillDependency{SkillName: "b"})
		manifestSkill(t, s, "b", SkillDependency{SkillName: "a"})
		p, err := s.PreviewDependencies(context.Background(), []tasksvc.SkillBinding{binding(a)})
		if err != nil || p.Resolvable || !hasIssue(p, "cycle") {
			t.Fatal(p, err)
		}
	})
	t.Run("hash", func(t *testing.T) {
		s := newSvc(t)
		ctx := context.Background()
		b := manifestSkill(t, s, "b")
		hash, _ := s.SkillContentSHA256(ctx, b.CurrentVersion.ID)
		a := manifestSkill(t, s, "a", SkillDependency{SkillName: "b", ContentSHA256: hash})
		newest := importSkill(t, s, "b", map[string]string{"SKILL.md": "---\nname: b\n---\nnew"})
		p, err := s.PreviewDependencies(ctx, []tasksvc.SkillBinding{binding(a)})
		if err != nil || !p.Resolvable || len(p.Additions) != 1 || p.Additions[0].VersionID != b.CurrentVersion.ID {
			t.Fatal(p, err)
		}
		p, err = s.PreviewDependencies(ctx, []tasksvc.SkillBinding{binding(a), {SkillID: newest.ID, Role: "auxiliary", Mode: "follow"}})
		if err != nil || p.Resolvable || !hasIssue(p, "version_conflict") {
			t.Fatal(p, err)
		}
	})
}

func TestManifestEditCreatesImmutableVersionAndRejectsStale(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	sk := importSkill(t, s, "main", map[string]string{"SKILL.md": "original content", "refs/data": "untouched"})
	before, _ := s.Files(ctx, sk.CurrentVersion.ID)
	view, err := s.GetSkillManifest(ctx, sk.ID, "")
	if err != nil || view.Manifest.Format != SkillManifestFormat {
		t.Fatal(view, err)
	}
	updated, err := s.SaveSkillManifest(ctx, sk.ID, view.VersionID, SkillManifest{Format: SkillManifestFormat, Environment: []RuntimeRequirement{{Kind: "env", Name: "TOKEN"}}})
	if err != nil || updated.VersionID == view.VersionID {
		t.Fatal(updated, err)
	}
	oldFiles, _ := s.Files(ctx, view.VersionID)
	newFiles, _ := s.Files(ctx, updated.VersionID)
	if !reflect.DeepEqual(before, oldFiles) || !bytes.Equal(newFiles["SKILL.md"], before["SKILL.md"]) || !bytes.Equal(newFiles["refs/data"], before["refs/data"]) {
		t.Fatal("existing content changed")
	}
	if _, err = s.SaveSkillManifest(ctx, sk.ID, view.VersionID, SkillManifest{Format: SkillManifestFormat}); !errors.Is(err, ErrManifestConflict) {
		t.Fatal(err)
	}
	current, _ := s.Ver.CurrentVersion(ctx, version.EntitySkill, sk.ID)
	if current.ID != updated.VersionID {
		t.Fatal("stale writer moved current")
	}
	old, err := s.ReadSkillManifest(ctx, view.VersionID)
	if err != nil || old.Format != "" {
		t.Fatal(old, err)
	}
}

func TestSetDetailsZIPAndLegacyPreservation(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	members := setMembers(t, s)
	details := SkillSetDetails{Description: "capability", Instructions: "follow the primary skill", InputHint: "source", OutputHint: "report", TestCases: []EvalCase{{Name: "smoke", Input: "sample", MustContain: []string{"done"}}}}
	set, err := s.SaveSetWithDetails(ctx, "", "package", "", members, &details)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := s.SaveSet(ctx, set.ID, "renamed", set.VersionID, members)
	if err != nil || !reflect.DeepEqual(edited.Details, details) {
		t.Fatal(edited, err)
	}
	raw, err := s.ExportSet(ctx, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "skill-set.json" {
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			var m setArchive
			_ = json.Unmarshal(b, &m)
			if m.Format != skillSetFormat || !reflect.DeepEqual(m.Details, details) {
				t.Fatal(string(b))
			}
		}
	}
	target := newSvc(t)
	imported, err := target.ImportSet(ctx, raw)
	if err != nil || !reflect.DeepEqual(imported.Details, details) {
		t.Fatal(imported, err)
	}
	for _, bad := range []SkillSetDetails{{TestCases: []EvalCase{{Name: "false pass", Input: "input"}}}, {TestCases: []EvalCase{{Name: "contradictory", Input: "input", MustContain: []string{"done"}, MustNotContain: []string{"done"}}}}} {
		if ValidateSkillSetDetails(bad) == nil {
			t.Fatal("invalid eval accepted")
		}
	}
}

func TestDependencyZIPSurvivesConflictRename(t *testing.T) {
	source := newSvc(t)
	ctx := context.Background()
	helper := manifestSkill(t, source, "helper")
	hash, _ := source.SkillContentSHA256(ctx, helper.CurrentVersion.ID)
	main := manifestSkill(t, source, "main", SkillDependency{SkillName: "helper", ContentSHA256: hash})
	set, err := source.SaveSet(ctx, "", "portable", "", []tasksvc.SkillBinding{binding(main), {SkillID: helper.ID, Mode: "follow", Role: "auxiliary"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := source.ExportSet(ctx, set.ID)
	target := newSvc(t)
	importSkill(t, target, "helper", map[string]string{"SKILL.md": "---\nname: helper\n---\nconflicting old content"})
	imported, err := target.ImportSet(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = target.ValidateDependencyBindings(ctx, imported.SkillBindings); err != nil {
		t.Fatal(err)
	}
	targetHash, _ := target.SkillContentSHA256(ctx, imported.SkillBindings[1].VersionID)
	if targetHash != hash {
		t.Fatal("portable hash changed")
	}
}
