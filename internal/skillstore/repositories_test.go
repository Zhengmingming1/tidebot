package skillstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func repositoryTestService(t *testing.T) *Service {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "repo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, version.New(database), nil, "")
}
func testRepository(t *testing.T, s *Service, url string) Repository {
	t.Helper()
	r, err := s.SaveRepository(context.Background(), Repository{URL: url, Ref: "main", Name: "Skills"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func repositoryTestFiles(body string) map[string][]byte {
	return map[string][]byte{"SKILL.md": []byte("---\nname: demo\ndescription: demo skill\n---\n" + body), "references/data.txt": []byte("shared content")}
}
func installRepositoryTest(t *testing.T, s *Service, r Repository, files map[string][]byte, current string) RepositoryInstallResult {
	t.Helper()
	res, err := s.InstallRepositorySkill(context.Background(), RepositoryInstallInput{RepositoryID: r.ID, RepositoryRevision: r.Revision, Path: "skills/demo", Commit: strings.Repeat("a", 40), TreeHash: RepositoryFilesHash(files), Name: "demo", Files: files, ExpectedCurrentVersionID: current})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func TestRepositoryCRUDAndFailedScan(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/team/skills.git")
	commit := strings.Repeat("a", 40)
	items := json.RawMessage(`[{"path":"skills/demo","name":"demo"}]`)
	if err := s.RecordRepositoryScan(ctx, r.ID, r.Revision, commit, items, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRepositoryScan(ctx, r.ID, r.Revision, "", nil, "authentication_failed"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRepository(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != commit || string(got.Candidates) != string(items) || got.LastScannedAt == "" || got.LastError != "authentication_failed" {
		t.Fatalf("lost last successful scan: %+v", got)
	}
	got.Name = "Renamed"
	got, err = s.SaveRepository(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != commit || got.Revision != 2 {
		t.Fatalf("rename invalidated scan: %+v", got)
	}
	if _, err = s.SaveRepository(ctx, r); !errors.Is(err, ErrRepositoryConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	if err = s.RecordRepositoryScan(ctx, r.ID, 1, strings.Repeat("b", 40), json.RawMessage(`[]`), ""); !errors.Is(err, ErrRepositoryConflict) {
		t.Fatalf("stale scan accepted: %v", err)
	}
	got.Root = "skills"
	got, err = s.SaveRepository(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != "" || string(got.Candidates) != "[]" {
		t.Fatalf("scope change retained stale scan: %+v", got)
	}
	list, err := s.ListRepositories(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err = s.DeleteRepository(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetRepository(ctx, r.ID); !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatal(err)
	}
}
func TestRepositoryInstallUpdateApplyAndLocalChanges(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/team/skills.git")
	oldFiles := repositoryTestFiles("old")
	first := installRepositoryTest(t, s, r, oldFiles, "")
	if !first.Created || !first.Applied || first.Source.BaseVersionID != first.VersionID || first.CurrentVersionID != first.VersionID {
		t.Fatalf("initial result: %+v", first)
	}
	same := installRepositoryTest(t, s, r, oldFiles, first.VersionID)
	if same.Created || same.VersionID != first.VersionID {
		t.Fatalf("not idempotent: %+v", same)
	}
	update := installRepositoryTest(t, s, r, repositoryTestFiles("upstream"), first.VersionID)
	if !update.Created || update.Applied || update.Source.BaseVersionID != first.VersionID || update.CurrentVersionID != first.VersionID || !update.Source.UpdateAvailable {
		t.Fatalf("download changed current/baseline: %+v", update)
	}
	local, _, err := s.CommitFiles(ctx, first.SkillID, "demo", repositoryTestFiles("local"), "user_edit", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.RepositorySourceForSkill(ctx, first.SkillID)
	if err != nil || !source.LocalModified || source.BaseVersionID != first.VersionID {
		t.Fatalf("local change lost: %+v %v", source, err)
	}
	if err = s.ApplyRepositoryVersion(ctx, first.SkillID, update.VersionID, first.VersionID); !errors.Is(err, ErrRepositoryConflict) {
		t.Fatalf("stale apply overwrote local: %v", err)
	}
	if err = s.ApplyRepositoryVersion(ctx, first.SkillID, update.VersionID, local.ID); err != nil {
		t.Fatal(err)
	}
	source, _ = s.RepositorySourceForSkill(ctx, first.SkillID)
	if source.LocalModified || source.UpdateAvailable || source.BaseVersionID != update.VersionID {
		t.Fatalf("adoption baseline not advanced: %+v", source)
	}
	if err = s.Rollback(ctx, first.SkillID, first.VersionID, "user"); err != nil {
		t.Fatal(err)
	}
	source, _ = s.RepositorySourceForSkill(ctx, first.SkillID)
	if !source.LocalModified || source.CurrentVersionID != first.VersionID {
		t.Fatalf("rollback lost: %+v", source)
	}
	// Re-importing older upstream content reuses history and keeps its immutable provenance.
	older := installRepositoryTest(t, s, r, oldFiles, first.VersionID)
	if older.Created || older.VersionID != first.VersionID {
		t.Fatalf("history not reused: %+v", older)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM versions WHERE entity_id=?`, first.SkillID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("version count %d: %v", count, err)
	}
	files, err := s.Files(ctx, first.VersionID)
	if err != nil || string(files["references/data.txt"]) != "shared content" {
		t.Fatalf("resources missing: %+v %v", files, err)
	}
}
func TestRepositorySameNameDifferentSourceAndCopy(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	a := testRepository(t, s, "https://example.test/a.git")
	b := testRepository(t, s, "ssh://git@example.test/b.git")
	first := installRepositoryTest(t, s, a, repositoryTestFiles("one"), "")
	input := RepositoryInstallInput{RepositoryID: b.ID, Path: "skills/demo", Name: "demo", Files: repositoryTestFiles("two"), Commit: strings.Repeat("b", 40)}
	_, err := s.InstallRepositorySkill(ctx, input)
	var conflict *RepositoryNameConflict
	if !errors.As(err, &conflict) || conflict.SuggestedName == "" {
		t.Fatalf("name collision not detected: %v", err)
	}
	input.Name = conflict.SuggestedName
	second, err := s.InstallRepositorySkill(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.SkillID == first.SkillID {
		t.Fatal("different source merged")
	}
	cur, _ := s.Ver.CurrentVersion(ctx, version.EntitySkill, first.SkillID)
	if cur.ID != first.VersionID {
		t.Fatal("original overwritten")
	}
	input.RepositoryID = a.ID
	input.Name = "demo-copy"
	input.Copy = true
	third, err := s.InstallRepositorySkill(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if third.Source != nil || third.SkillID == first.SkillID {
		t.Fatalf("copy relinked source: %+v", third)
	}
}
func TestRepositoryDeletionPreservesSkillsAndReaddition(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/a.git")
	files := repositoryTestFiles("old")
	first := installRepositoryTest(t, s, r, files, "")
	if err := s.DeleteRepository(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	source, err := s.RepositorySourceForSkill(ctx, first.SkillID)
	if err != nil || !source.SourceRemoved || source.URL != r.URL {
		t.Fatalf("source lost: %+v %v", source, err)
	}
	cur, err := s.Ver.CurrentVersion(ctx, version.EntitySkill, first.SkillID)
	if err != nil || cur.ID != first.VersionID {
		t.Fatalf("installed content removed: %v", err)
	}
	r2 := testRepository(t, s, r.URL)
	sources, err := s.ListRepositorySources(ctx, r2.ID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("re-added repo cannot discover installed skill: %+v %v", sources, err)
	}
	again := installRepositoryTest(t, s, r2, files, first.VersionID)
	if again.SkillID != first.SkillID || again.Source.RepositoryID != r2.ID {
		t.Fatalf("re-add duplicated skill: %+v", again)
	}
}
func TestRepositoryImportRollbackOnWriteFailure(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/a.git")
	_, err := s.DB.Exec(`CREATE TRIGGER fail_repository_binding BEFORE INSERT ON skill_repository_bindings BEGIN SELECT RAISE(ABORT,'injected write failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InstallRepositorySkill(ctx, RepositoryInstallInput{RepositoryID: r.ID, Path: "skills/demo", Name: "demo", Commit: strings.Repeat("a", 40), Files: repositoryTestFiles("old")})
	if err == nil {
		t.Fatal("injected error ignored")
	}
	for _, table := range []string{"skills", "versions", "version_files", "blobs", "pointers", "operation_log", "skill_repository_bindings"} {
		var n int
		if e := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatalf("partial import in %s: %d %v", table, n, e)
		}
	}
}
func TestRepositoryImportValidatesFilesAndConfigRevision(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/a.git")
	input := RepositoryInstallInput{RepositoryID: r.ID, RepositoryRevision: r.Revision, Name: "demo", Path: "skills/demo", Commit: strings.Repeat("a", 40), Files: repositoryTestFiles("old")}
	input.Files["../escape"] = []byte("x")
	if _, err := s.InstallRepositorySkill(ctx, input); err == nil {
		t.Fatal("accepted traversal")
	}
	delete(input.Files, "../escape")
	input.Files["skill.md"] = []byte("alias")
	if _, err := s.InstallRepositorySkill(ctx, input); err == nil {
		t.Fatal("accepted case collision")
	}
	delete(input.Files, "skill.md")
	r.Ref = "release"
	if _, err := s.SaveRepository(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallRepositorySkill(ctx, input); !errors.Is(err, ErrRepositoryConflict) {
		t.Fatalf("old connection preview accepted: %v", err)
	}
}
func TestRepositoryReusesExistingVersionWithoutRewritingProvenance(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/a.git")
	first := installRepositoryTest(t, s, r, repositoryTestFiles("old"), "")
	localFiles := repositoryTestFiles("local")
	local, _, err := s.CommitFiles(ctx, first.SkillID, "demo", localFiles, "user_edit", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	next := installRepositoryTest(t, s, r, localFiles, local.ID)
	if next.Created || next.VersionID != local.ID {
		t.Fatalf("matching local version duplicated: %+v", next)
	}
	var source string
	var ref sql.NullString
	if err = s.DB.QueryRow(`SELECT source,source_ref FROM versions WHERE id=?`, local.ID).Scan(&source, &ref); err != nil {
		t.Fatal(err)
	}
	if source != "user_edit" || ref.Valid {
		t.Fatalf("immutable provenance rewritten: %s %+v", source, ref)
	}
	if next.Source.BaseVersionID != first.VersionID {
		t.Fatal("download silently adopted local version")
	}
	if err = s.ApplyRepositoryVersion(ctx, first.SkillID, local.ID, local.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := s.RepositorySourceForSkill(ctx, first.SkillID)
	if b.BaseVersionID != local.ID || b.LocalModified {
		t.Fatalf("explicit adoption failed: %+v", b)
	}
}

func TestRepositoryLocalOnlyChangeIsNotAnUpstreamUpdate(t *testing.T) {
	ctx := context.Background()
	s := repositoryTestService(t)
	r := testRepository(t, s, "https://example.test/a.git")
	first := installRepositoryTest(t, s, r, repositoryTestFiles("old"), "")
	if _, _, err := s.CommitFiles(ctx, first.SkillID, "demo", repositoryTestFiles("local"), "user_edit", "user", false); err != nil {
		t.Fatal(err)
	}
	source, err := s.RepositorySourceForSkill(ctx, first.SkillID)
	if err != nil || !source.LocalModified || source.UpdateAvailable {
		t.Fatalf("local-only change mistaken for remote update: %+v %v", source, err)
	}
}
