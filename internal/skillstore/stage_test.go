package skillstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	ver := version.New(sqlDB)
	return New(sqlDB, ver, tasksvc.New(db.NewStore(sqlDB), ver, nil), "")
}

func importSkill(t *testing.T, s *Service, name string, files map[string]string) SkillView {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o700)
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sk, err := s.ImportDir(context.Background(), name, dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	return sk
}

func TestStageWritesPluginLayout(t *testing.T) {
	s := newSvc(t)
	sk := importSkill(t, s, "Code Review", map[string]string{
		"SKILL.md":       "---\nname: Code Review!\ndescription: Review code\n---\nReview $ARGUMENTS\n",
		"refs/notes.txt": "n",
	})
	root := t.TempDir()
	got, err := s.Stage(context.Background(), root, []tasksvc.SkillBinding{{SkillID: sk.ID, Mode: "follow"}, {SkillID: sk.ID, Mode: "follow"}})
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if got[0].Name != "code-review" || got[1].Name != "code-review-2" || got[0].Command() != "/tidebot:code-review" || got[0].Description != "Review code" {
		t.Fatalf("%+v", got)
	}
	for _, p := range []string{".claude-plugin/plugin.json", "skills/code-review/SKILL.md", "skills/code-review/refs/notes.txt"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Fatal(p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Fatal("stage must not create a .claude folder")
	}
}

func TestStageRejectsUnsafePaths(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	id, _ := s.ensureSkill(ctx, "evil")
	v, _, err := s.CommitFiles(ctx, id, "evil", map[string][]byte{"SKILL.md": []byte("x"), "../../escape.txt": []byte("x")}, "user", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "stage")
	if _, err := s.Stage(ctx, root, []tasksvc.SkillBinding{{SkillID: id, Mode: "pinned", VersionID: v.ID}}); err == nil {
		t.Fatal("unsafe path accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote outside the stage")
	}
}

func TestCleanLegacyCopies(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	files := map[string]string{"SKILL.md": "---\nname: hello\n---\nHi\n", "a/b.txt": "b"}
	importSkill(t, s, "hello", files)
	importSkill(t, s, "other", map[string]string{"SKILL.md": "other\n"})
	ws := t.TempDir()
	base := filepath.Join(ws, ".claude", "skills")
	write := func(dir string, fs map[string]string) {
		for p, c := range fs {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(base, dir, p)), 0o700)
			_ = os.WriteFile(filepath.Join(base, dir, p), []byte(c), 0o600)
		}
	}
	write("hello", files)                                                                                     // exact tidebot copy: removed
	write("hello-edited", map[string]string{"SKILL.md": "---\nname: hello\n---\nHi there\n", "a/b.txt": "b"}) // edited: kept
	write("extra", map[string]string{"SKILL.md": "other\n", "mine.md": "user file"})                          // extra file: kept
	write("users-own", map[string]string{"SKILL.md": "my own skill\n"})                                       // user's: kept
	res, err := s.CleanLegacyCopies(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, r := range res {
		removed[filepath.Base(r.Path)] = r.Removed
	}
	if !removed["hello"] || removed["hello-edited"] || removed["extra"] || removed["users-own"] || len(res) != 4 {
		t.Fatalf("%+v", res)
	}
	for _, d := range []string{"hello-edited", "extra", "users-own"} {
		if _, err := os.Stat(filepath.Join(base, d, "SKILL.md")); err != nil {
			t.Fatal(d, "was touched")
		}
	}
	// Only tidebot copies there: the empty .claude/skills and .claude go too.
	ws2 := t.TempDir()
	base = filepath.Join(ws2, ".claude", "skills")
	write("hello", files)
	if _, err := s.CleanLegacyCopies(ctx, ws2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws2, ".claude")); !os.IsNotExist(err) {
		t.Fatal(".claude left behind")
	}
	// A .claude holding anything else (the user's settings) stays.
	ws3 := t.TempDir()
	base = filepath.Join(ws3, ".claude", "skills")
	write("hello", files)
	_ = os.WriteFile(filepath.Join(ws3, ".claude", "settings.json"), []byte("{}"), 0o600)
	if _, err := s.CleanLegacyCopies(ctx, ws3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws3, ".claude", "settings.json")); err != nil {
		t.Fatal("user's settings touched")
	}
	// No .claude at all: nothing to do.
	if res, err := s.CleanLegacyCopies(ctx, t.TempDir()); err != nil || len(res) != 0 {
		t.Fatal(res, err)
	}
}
