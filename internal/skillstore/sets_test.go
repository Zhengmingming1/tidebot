package skillstore

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

func setMembers(t *testing.T, s *Service) []tasksvc.SkillBinding {
	t.Helper()
	out := []tasksvc.SkillBinding{}
	for _, name := range []string{"primary", "helper"} {
		sk := importSkill(t, s, name, map[string]string{"SKILL.md": "---\nname: " + name + "\n---\n# " + name, "refs/data.txt": name})
		out = append(out, tasksvc.SkillBinding{SkillID: sk.ID, Mode: "follow"})
	}
	return out
}

func TestSkillSetVersionsAndResolution(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	members := setMembers(t, s)
	set, err := s.SaveSet(ctx, "", "Research", "", members)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.SkillBindings) != 2 || set.SkillBindings[0].Role != "primary" || set.SkillBindings[1].Mode != "pinned" {
		t.Fatalf("%+v", set)
	}
	frozen := set.SkillBindings[1].VersionID
	if _, _, err = s.CommitFiles(ctx, members[1].SkillID, "helper", map[string][]byte{"SKILL.md": []byte("---\nname: helper\n---\nnew content")}, "test", "test", false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSet(ctx, set.ID)
	if got.SkillBindings[1].VersionID != frozen {
		t.Fatal("saved set changed with current skill")
	}
	if _, err = s.SaveSet(ctx, set.ID, "Changed", "", members); !errors.Is(err, ErrSetConflict) {
		t.Fatalf("missing expected version: %v", err)
	}
	v2, err := s.SaveSet(ctx, set.ID, "Changed", set.VersionID, members)
	if err != nil || v2.VersionNo != 2 {
		t.Fatalf("update %+v %v", v2, err)
	}
	if _, err = s.SaveSet(ctx, set.ID, "Stale", set.VersionID, members); !errors.Is(err, ErrSetConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if _, err = s.ResolveBindings(ctx, []tasksvc.SkillBinding{{SkillID: members[0].SkillID, Mode: "pinned", VersionID: frozen}}); err == nil {
		t.Fatal("cross-skill version accepted")
	}
	if err = s.DeleteSet(ctx, set.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetSet(ctx, set.ID); err == nil {
		t.Fatal("deleted set listed")
	}
	if _, err = s.Ver.GetVersion(ctx, set.VersionID); err != nil {
		t.Fatal("deleting set removed immutable history")
	}
	if _, err = s.ResolveBindings(ctx, set.SkillBindings); err != nil {
		t.Fatal("deleting set invalidated applied bindings", err)
	}
}

func TestSkillSetArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := newSvc(t)
	set, err := source.SaveSet(ctx, "", "Portable", "", setMembers(t, source))
	if err != nil {
		t.Fatal(err)
	}
	data, err := source.ExportSet(ctx, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	dest := newSvc(t)
	conflict := importSkill(t, dest, "primary", map[string]string{"SKILL.md": "---\nname: primary\n---\nmy different skill"})
	imported, err := dest.ImportSet(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if imported.SkillBindings[0].SkillID == conflict.ID {
		t.Fatal("import overwrote a same-name skill")
	}
	now, _ := dest.ResolveVersion(ctx, tasksvc.SkillBinding{SkillID: conflict.ID, Mode: "follow"})
	if now != conflict.CurrentVersion.ID {
		t.Fatal("existing current pointer changed")
	}
	for i, b := range imported.SkillBindings {
		files, _ := dest.Files(ctx, b.VersionID)
		want, _ := source.Files(ctx, set.SkillBindings[i].VersionID)
		if !bytes.Equal(files["refs/data.txt"], want["refs/data.txt"]) || b.Role != set.SkillBindings[i].Role {
			t.Fatalf("member %d lost content or role", i)
		}
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../SKILL.md")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	if _, err = dest.ImportSet(ctx, buf.Bytes()); err == nil {
		t.Fatal("zip-slip accepted")
	}
}

func TestStageRejectsDuplicateCommands(t *testing.T) {
	s := newSvc(t)
	a := importSkill(t, s, "one", map[string]string{"SKILL.md": "---\nname: shared\n---\nA"})
	b := importSkill(t, s, "two", map[string]string{"SKILL.md": "---\nname: shared\n---\nB"})
	bindings := []tasksvc.SkillBinding{{SkillID: a.ID}, {SkillID: b.ID}}
	if _, err := s.Stage(context.Background(), t.TempDir(), bindings); err == nil {
		t.Fatal("duplicate Claude command names accepted")
	}
	if _, err := s.SaveSet(context.Background(), "", "Duplicate", "", bindings); err == nil {
		t.Fatal("ambiguous set saved")
	}
}

func TestSetImportRejectsInvalidBeforeWriting(t *testing.T) {
	s := newSvc(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range map[string]string{"skill-set.json": `{"format":"tidebot-skill-set/1","name":"bad","members":[{"skill_id":"one","mode":"follow"},{"skill_id":"two","mode":"follow"}]}`, "skills/one/SKILL.md": "# One", "skills/two/README.md": "missing"} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	if _, err := s.ImportSet(context.Background(), buf.Bytes()); err == nil {
		t.Fatal("missing member accepted")
	}
	all, _ := s.List(context.Background())
	if len(all) != 0 {
		t.Fatal("part of invalid bundle was imported")
	}
	// A matching skill's support files stay together when staged.
	members := setMembers(t, s)
	root := t.TempDir()
	st, err := s.Stage(context.Background(), root, members)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(st[1].Dir, "refs", "data.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillSetImportReportsPartialAndCanRetry(t *testing.T) {
	ctx := context.Background()
	src := newSvc(t)
	set, err := src.SaveSet(ctx, "", "Portable", "", setMembers(t, src))
	if err != nil {
		t.Fatal(err)
	}
	data, err := src.ExportSet(ctx, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	dst := newSvc(t)
	original := importSkill(t, dst, "primary", map[string]string{"SKILL.md": "# Preserve this different skill"})
	if _, err = dst.DB.Exec(`CREATE TRIGGER fail_set BEFORE INSERT ON skill_sets BEGIN SELECT RAISE(ABORT,'fixture set write failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = dst.ImportSet(ctx, data)
	var partial *PartialSetImportError
	if !errors.As(err, &partial) || len(partial.ImportedSkillIDs) != 2 {
		t.Fatalf("missing partial result: %+v %v", partial, err)
	}
	still, _ := dst.ResolveVersion(ctx, tasksvc.SkillBinding{SkillID: original.ID})
	if still != original.CurrentVersion.ID {
		t.Fatal("overwrote existing skill")
	}
	all, _ := dst.List(ctx)
	if len(all) != 3 {
		t.Fatalf("created members not visible: %d", len(all))
	}
	if sets, _ := dst.ListSets(ctx); len(sets) != 0 {
		t.Fatal("failed composition remained visible")
	}
	if _, err = dst.DB.Exec(`DROP TRIGGER fail_set`); err != nil {
		t.Fatal(err)
	}
	retry, err := dst.ImportSet(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	all, _ = dst.List(ctx)
	if len(all) != 3 {
		t.Fatal("retry duplicated already imported content")
	}
	for i, b := range retry.SkillBindings {
		if b.SkillID != partial.ImportedSkillIDs[i] {
			t.Fatalf("retry did not reuse member %d", i)
		}
	}
}
