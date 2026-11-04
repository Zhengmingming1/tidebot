package capability

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func installOwnedSkill(t *testing.T, s *Store) (Record, *skillstore.Service) {
	t.Helper()
	svc := skillstore.New(s.DB, version.New(s.DB), nil, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Example\nUseful instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	skill, err := svc.ImportDir(context.Background(), "Owned capability skill", dir, "capability:ui")
	if err != nil {
		t.Fatal(err)
	}
	l := testLock()
	l.Components[0].SkillID = skill.ID
	l.Components[0].VersionID = skill.CurrentVersion.ID
	r, err := s.Install(context.Background(), l, testFiles())
	if err != nil {
		t.Fatal(err)
	}
	return r, svc
}
func TestUninstallRemovesExclusiveRegistrationAndRestoresRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first, svc := installOwnedSkill(t, s)
	c := first.Lock.Components[0]
	out, err := s.UninstallWithReport(ctx, first.ID, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != c.SkillID {
		t.Fatalf("report: %+v", out)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, c.SkillID).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if files, e := svc.Files(ctx, c.VersionID); e != nil || len(files) == 0 {
		t.Fatal("immutable files lost", e)
	}
	if err = s.Verify(ctx, first.ID, first.VersionID); err != nil {
		t.Fatal(err)
	}
	// A reinstall can use a new registration, then roll back to retained history.
	l := testLock()
	if _, err = s.Install(ctx, l, testFiles()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Rollback(ctx, first.ID, first.VersionID, "ui"); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, c.SkillID).Scan(&n); err != nil || n != 1 {
		t.Fatal("rollback did not restore registration", n, err)
	}
	var pointer string
	if err = s.DB.QueryRow(`SELECT version_id FROM pointers WHERE entity_type='skill' AND entity_id=? AND name='current'`, c.SkillID).Scan(&pointer); err != nil || pointer != c.VersionID {
		t.Fatal(pointer, err)
	}
}
func TestUninstallRetainsSharedAndIndependentSkills(t *testing.T) {
	for _, kind := range []string{"shared", "user_edit", "custom_settings", "renamed", "run", "capability_run", "historical_task", "tag"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			r, svc := installOwnedSkill(t, s)
			c := r.Lock.Components[0]
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := s.DB.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "shared":
				l := r.Lock
				l.ID = "other"
				if _, err := s.Install(ctx, l, testFiles()); err != nil {
					t.Fatal(err)
				}
			case "user_edit":
				if _, _, err := svc.CommitFiles(ctx, c.SkillID, "Owned capability skill", map[string][]byte{"SKILL.md": []byte("Changed")}, "user_edit", "user", false); err != nil {
					t.Fatal(err)
				}
			case "custom_settings":
				exec(`UPDATE skills SET optimize_mode='manual' WHERE id=?`, c.SkillID)
			case "renamed":
				exec(`UPDATE skills SET name='My renamed skill' WHERE id=?`, c.SkillID)
			case "run":
				exec(`INSERT INTO runs(id,status,created_at) VALUES('run','completed','now')`)
				exec(`INSERT INTO run_skill_bindings(run_id,skill_id,version_id,skill_name,role,position) VALUES('run',?,?,'Owned','primary',0)`, c.SkillID, c.VersionID)
			case "capability_run":
				exec(`INSERT INTO runs(id,status,created_at) VALUES('run','completed','now')`)
				exec(`INSERT INTO capability_run_bindings VALUES('run',?,?)`, r.ID, r.VersionID)
			case "historical_task":
				_, err := version.New(s.DB).Commit(ctx, version.CommitInput{EntityType: "task", EntityID: "old", Content: map[string]any{"skill": c.SkillID}, NoMove: true})
				if err != nil {
					t.Fatal(err)
				}
			case "tag":
				exec(`INSERT INTO version_tags VALUES('skill',?,'keep',?,'now')`, c.SkillID, c.VersionID)
			}
			out, err := s.UninstallWithReport(ctx, r.ID, "ui")
			if err != nil {
				t.Fatal(err)
			}
			if len(out.RemovedSkills) != 0 {
				t.Fatalf("removed referenced skill: %+v", out)
			}
			found := false
			for _, retained := range out.Retained {
				if retained.Kind == "skill" && retained.ID == c.SkillID {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing retention reason: %+v", out)
			}
			var n int
			if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, c.SkillID).Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		})
	}
}

func TestUninstallCleanupFailureRollsBackRegistration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	c := r.Lock.Components[0]
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_uninstall BEFORE UPDATE OF status ON capabilities WHEN NEW.status='uninstalled' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	if err == nil || out.OK || len(out.RemovedSkills) != 0 {
		t.Fatal("failed transaction reported success", out, err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, c.SkillID).Scan(&n); err != nil || n != 1 {
		t.Fatal("registration not restored", n, err)
	}
	if _, err = s.Get(ctx, r.ID); err != nil {
		t.Fatal("availability changed despite rollback", err)
	}
}
