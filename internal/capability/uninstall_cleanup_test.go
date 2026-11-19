package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestUninstallHistoryScanSkipsUnrelatedDefinitions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	c := r.Lock.Components[0]

	// Large backlog of workflow definitions that do not mention this skill.
	// Before the instr filter, uninstall parsed every blob for each owned skill.
	blob := `{"nodes":[{"id":"n1","skill":"unrelated-skill-aaaaaaaa","prompt":"` + strings.Repeat("x", 32*1024) + `"}]}`
	const n = 80
	for i := 0; i < n; i++ {
		wf := "wf-hist-" + string(rune('a'+i%26)) + "-" + itoa(i)
		if _, err := s.DB.Exec(`INSERT INTO workflows(id,name,description,version,deleted,created_at,updated_at) VALUES(?,?,?,1,0,'t','t')`, wf, wf, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES(?,1,?,?)`, wf, blob, "t"); err != nil {
			t.Fatal(err)
		}
	}
	// One historical task version that does reference the skill — must retain.
	if _, err := version.New(s.DB).Commit(ctx, version.CommitInput{EntityType: "task", EntityID: "old", Content: map[string]any{"skill": c.SkillID}, NoMove: true}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RemovedSkills) != 0 {
		t.Fatalf("should retain skill referenced by historical task: %+v", out)
	}
	found := false
	for _, retained := range out.Retained {
		if retained.Kind == "skill" && retained.ID == c.SkillID && retained.Reason == "retained by task, workflow, session or proposal history" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing history retention: %+v", out)
	}
	t.Logf("uninstall with %d unrelated %dKB workflow definitions: %s", n, len(blob)/1024, elapsed)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestUninstallHistoryScanRemovesWhenNoReference(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	c := r.Lock.Components[0]
	blob := `{"nodes":[{"id":"n1","skill":"other","prompt":"` + strings.Repeat("y", 8*1024) + `"}]}`
	for i := 0; i < 40; i++ {
		wf := "wf-clean-" + itoa(i)
		if _, err := s.DB.Exec(`INSERT INTO workflows(id,name,description,version,deleted,created_at,updated_at) VALUES(?,?,?,1,0,'t','t')`, wf, wf, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES(?,1,?,?)`, wf, blob, "t"); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != c.SkillID {
		t.Fatalf("expected exclusive skill removed: %+v", out)
	}
	t.Logf("uninstall with 40 unrelated definitions (no ref): %s", elapsed)
}

func TestUninstallHistoryScanPerf(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, _ = installOwnedSkill(t, s) // ensure schema / tables exist
	blob := `{"nodes":[{"id":"n1","skill":"unrelated-skill-aaaaaaaa","prompt":"` + strings.Repeat("x", 32*1024) + `"}]}`
	const n = 80
	for i := 0; i < n; i++ {
		wf := "wf-perf-" + itoa(i)
		if _, err := s.DB.Exec(`INSERT INTO workflows(id,name,description,version,deleted,created_at,updated_at) VALUES(?,?,?,1,0,'t','t')`, wf, wf, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES(?,1,?,?)`, wf, blob, "t"); err != nil {
			t.Fatal(err)
		}
	}
	const skills = 8
	candidates := map[string]bool{}
	for i := 0; i < skills; i++ {
		candidates["sk-perf-"+itoa(i)+"-"+strings.Repeat("a", 24)] = true
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	oldStart := time.Now()
	oldBytes := 0
	oldRows := 0
	for sid := range candidates {
		one := map[string]bool{sid: true}
		rows, err := tx.Query(`SELECT definition_json FROM workflow_versions`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			oldBytes += len(raw)
			oldRows++
			var tree any // the old per-skill full parse
			_ = json.Unmarshal([]byte(raw), &tree)
			_ = one
		}
		rows.Close()
	}
	oldElapsed := time.Since(oldStart)

	newStart := time.Now()
	found, err := skillsReferencedInHistory(ctx, tx, candidates)
	newElapsed := time.Since(newStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("unexpected hits %+v", found)
	}
	t.Logf("%d skills × %d×32KB unrelated defs: old %s (%d row reads, %d KB into Go); new one-pass escape-aware gate %s",
		skills, n, oldElapsed, oldRows, oldBytes/1024, newElapsed)
	if newElapsed*3 > oldElapsed {
		t.Fatalf("expected ≥3× speedup, got old=%s new=%s", oldElapsed, newElapsed)
	}
}

func TestSkillsReferencedInHistorySubstringFalsePositive(t *testing.T) {
	// Substring hit that is not a JSON string leaf must not retain.
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	c := r.Lock.Components[0]
	// Embed the skill id inside a longer token so Contains matches but JSON leaf does not.
	raw := `{"note":"prefix-` + c.SkillID + `-suffix"}`
	if _, err := s.DB.Exec(`INSERT INTO workflows(id,name,description,version,deleted,created_at,updated_at) VALUES('wf-fp','fp','',1,0,'t','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf-fp',1,?,?)`, raw, "t"); err != nil {
		t.Fatal(err)
	}
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != c.SkillID {
		t.Fatalf("false-positive substring retained skill: %+v", out)
	}
}

func jsonUnicodeEscape(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		fmt.Fprintf(&b, `\u%04x`, id[i])
	}
	return b.String()
}

func assertHistoryRetention(t *testing.T, out UninstallResult, skillID, why string) {
	t.Helper()
	if len(out.RemovedSkills) != 0 {
		t.Fatalf("%s: skill was removed: %+v", why, out)
	}
	for _, retained := range out.Retained {
		if retained.Kind == "skill" && retained.ID == skillID && retained.Reason == "retained by task, workflow, session or proposal history" {
			return
		}
	}
	t.Fatalf("%s: missing history retention: %+v", why, out)
}

func TestUninstallHistoryScanRetainsEscapedSkillID(t *testing.T) {
	// A historical definition can name the skill with JSON \u escapes (the
	// active-path gate already handled this; history used to look only for the
	// literal id and would have deleted the registration).
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	c := r.Lock.Components[0]
	raw := `{"skills":["` + jsonUnicodeEscape(c.SkillID) + `"]}`
	if strings.Contains(raw, c.SkillID) {
		t.Fatal("fixture still contains the literal id")
	}
	// Only an older version names the skill, so the active-path check (current
	// definitions only) lets uninstall proceed; history must still retain it.
	if _, err := s.DB.Exec(`INSERT INTO workflows(id,name,description,version,deleted,created_at,updated_at) VALUES('wf-esc','esc','',2,0,'t','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf-esc',1,?,?)`, raw, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf-esc',2,?,?)`, `{"skills":["sk_other"]}`, "t"); err != nil {
		t.Fatal(err)
	}
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryRetention(t, out, c.SkillID, "escaped skill id")
}

func TestUninstallHistoryScanIgnoresHTMLEscapesWithoutSkill(t *testing.T) {
	// Go's json.Marshal writes < and & as \u003c / \u0026. Those must not
	// make history retain a skill the definition does not name.
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	c := r.Lock.Components[0]
	raw, _ := json.Marshal(map[string]any{"prompt": "检查 <b>&</b>", "skills": []string{"sk_other"}})
	if !strings.Contains(string(raw), `\u00`) {
		t.Fatalf("expected HTML escapes in fixture: %s", raw)
	}
	if _, err := s.DB.Exec(`INSERT INTO workflows(id,name,description,version,deleted,created_at,updated_at) VALUES('wf-html','html','',1,0,'t','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO workflow_versions(workflow_id,version,definition_json,created_at) VALUES('wf-html',1,?,?)`, string(raw), "t"); err != nil {
		t.Fatal(err)
	}
	out, err := s.UninstallWithReport(ctx, r.ID, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != c.SkillID {
		t.Fatalf("HTML escapes in an unrelated prompt retained the skill: %+v", out)
	}
}

func insertCapabilityVersion(t *testing.T, s *Store, capID, verID, lockJSON, hash string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM capabilities WHERE id=?`, capID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		if _, err := s.DB.Exec(`INSERT INTO capabilities(id,title,status,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, capID, capID, "installed", verID, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if hash == "" {
		hash = digest([]byte(lockJSON))
	}
	if _, err := s.DB.Exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES(?,?,?,?,?,?)`, verID, capID, lockJSON, hash, "{}", now); err != nil {
		t.Fatal(err)
	}
}

func TestAddOwnLockSkillsIntegrity(t *testing.T) {
	raw := `{"components":[{"skill_id":"sk_a","version_id":"ver_a"},{"skill_id":"","version_id":"ver_empty"}]}`
	good := digest([]byte(raw))
	own := map[string]map[string]bool{}
	if err := addOwnLockSkills(raw, good, own); err != nil {
		t.Fatal(err)
	}
	if !own["sk_a"]["ver_a"] || len(own) != 1 || own["sk_a"][""] {
		t.Fatalf("own=%v", own)
	}
	if err := addOwnLockSkills(raw, strings.Repeat("0", 64), map[string]map[string]bool{}); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("own lock with bad digest accepted: %v", err)
	}
	if err := addOwnLockSkills("{", good, map[string]map[string]bool{}); err == nil {
		t.Fatal("malformed JSON of this package was accepted")
	}
}

func TestUninstallIgnoresOtherLockIntegrity(t *testing.T) {
	for _, kind := range []string{"shared", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			r, _ := installOwnedSkill(t, s)
			c := r.Lock.Components[0]
			skill := c.SkillID
			if kind == "unrelated" {
				skill = "sk_other_unrelated"
			}
			lock := `{"format":"tidebot-capability/1","id":"other","title":"Other","components":[{"type":"skill","name":"x","skill_id":"` + skill + `","version_id":"ver_x"}]}`
			insertCapabilityVersion(t, s, "other", "other-ver", lock, strings.Repeat("0", 64))
			out, err := s.UninstallWithReport(ctx, r.ID, "ui")
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, c.SkillID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if kind == "shared" {
				if n != 1 || len(out.RemovedSkills) != 0 {
					t.Fatalf("shared skill not retained: n=%d out=%+v", n, out)
				}
				found := false
				for _, retained := range out.Retained {
					if retained.Kind == "skill" && retained.ID == c.SkillID && retained.Reason == "referenced by another capability snapshot" {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing shared retention: %+v", out)
				}
				return
			}
			if n != 0 || len(out.RemovedSkills) != 1 || out.RemovedSkills[0] != c.SkillID {
				t.Fatalf("exclusive skill not removed: n=%d out=%+v", n, out)
			}
		})
	}
}

func TestUninstallRefusesOwnLockIntegrityMismatch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, _ := installOwnedSkill(t, s)
	sid := r.Lock.Components[0].SkillID
	raw, err := json.Marshal(r.Lock)
	if err != nil {
		t.Fatal(err)
	}
	insertCapabilityVersion(t, s, r.ID, "ver-bad", string(raw), strings.Repeat("0", 64))
	if _, err = s.UninstallWithReport(ctx, r.ID, "ui"); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("want integrity refusal, got %v", err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM skills WHERE id=?`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatal("skill removed despite refusal", n, err)
	}
	var status string
	if err = s.DB.QueryRow(`SELECT status FROM capabilities WHERE id=?`, r.ID).Scan(&status); err != nil || status != "installed" {
		t.Fatal("capability changed despite refusal", status, err)
	}
}
