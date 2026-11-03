package skillstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

var ErrRepositoryConflict = errors.New("repository or current skill version changed; refresh the preview")
var ErrRepositoryNotFound = errors.New("skill repository not found")

// Repository stores the last successful scan separately from the most recent error.
type Repository struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	URL           string          `json:"url"`
	Ref           string          `json:"ref"`
	Root          string          `json:"root"`
	Revision      int             `json:"revision"`
	Commit        string          `json:"commit"`
	Candidates    json.RawMessage `json:"candidates"`
	LastScannedAt string          `json:"last_scanned_at"`
	LastError     string          `json:"last_error"`
	LastErrorCode string          `json:"last_error_code"`
	CreatedAt     string          `json:"created_at"`
}

const repositoryColumns = `id,name,url,ref,root,revision,commit_id,candidates_json,last_scanned_at,last_error,last_error_code,created_at`

type repositoryScanner interface{ Scan(...any) error }

func scanRepository(row repositoryScanner) (Repository, error) {
	var r Repository
	var candidates string
	err := row.Scan(&r.ID, &r.Name, &r.URL, &r.Ref, &r.Root, &r.Revision, &r.Commit, &candidates, &r.LastScannedAt, &r.LastError, &r.LastErrorCode, &r.CreatedAt)
	r.Candidates = json.RawMessage(candidates)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrRepositoryNotFound
	}
	return r, err
}
func (s *Service) ListRepositories(ctx context.Context) ([]Repository, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+repositoryColumns+` FROM skill_repositories ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repository{}
	for rows.Next() {
		r, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) GetRepository(ctx context.Context, repoID string) (Repository, error) {
	return scanRepository(s.DB.QueryRowContext(ctx, `SELECT `+repositoryColumns+` FROM skill_repositories WHERE id=?`, repoID))
}
func (s *Service) SaveRepository(ctx context.Context, r Repository) (Repository, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.URL = strings.TrimSpace(r.URL)
	r.Ref = strings.TrimSpace(r.Ref)
	r.Root = strings.Trim(strings.TrimSpace(r.Root), "/")
	if r.URL == "" || len(r.URL) > 2048 || strings.ContainsAny(r.URL, "\x00\r\n") || len(r.Ref) > 256 || strings.ContainsAny(r.Ref, "\x00\r\n") {
		return Repository{}, fmt.Errorf("invalid repository address or ref")
	}
	if r.Name == "" {
		r.Name = strings.TrimSuffix(path.Base(r.URL), ".git")
	}
	if len([]rune(r.Name)) > 128 {
		return Repository{}, fmt.Errorf("repository name too long")
	}
	if r.Root != "" {
		if clean, err := skillpkg.CleanRel(r.Root); err != nil || clean != r.Root {
			return Repository{}, fmt.Errorf("invalid scan directory")
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Repository{}, err
	}
	defer tx.Rollback()
	if r.ID == "" {
		r.ID = id.New("skillrepo")
		r.Revision = 1
		r.CreatedAt = nowUTC()
		_, err = tx.ExecContext(ctx, `INSERT INTO skill_repositories(id,name,url,ref,root,created_at) VALUES(?,?,?,?,?,?)`, r.ID, r.Name, r.URL, r.Ref, r.Root, r.CreatedAt)
	} else {
		old, e := scanRepository(tx.QueryRowContext(ctx, `SELECT `+repositoryColumns+` FROM skill_repositories WHERE id=?`, r.ID))
		if e != nil {
			return Repository{}, e
		}
		if old.Revision != r.Revision {
			return Repository{}, ErrRepositoryConflict
		}
		if old.URL != r.URL || old.Ref != r.Ref {
			if _, err = tx.ExecContext(ctx, `UPDATE skill_repository_bindings SET repository_id=NULL WHERE repository_id=?`, r.ID); err != nil {
				return Repository{}, err
			}
		}
		// Preserve successful scan on a rename; invalidate it when its scope changes.
		changed := old.URL != r.URL || old.Ref != r.Ref || old.Root != r.Root
		if changed {
			old.Commit = ""
			old.Candidates = json.RawMessage(`[]`)
			old.LastScannedAt = ""
			old.LastError = ""
			old.LastErrorCode = ""
		}
		_, err = tx.ExecContext(ctx, `UPDATE skill_repositories SET name=?,url=?,ref=?,root=?,revision=revision+1,commit_id=?,candidates_json=?,last_scanned_at=?,last_error=?,last_error_code=? WHERE id=?`, r.Name, r.URL, r.Ref, r.Root, old.Commit, string(old.Candidates), old.LastScannedAt, old.LastError, old.LastErrorCode, r.ID)
	}
	if err != nil {
		return Repository{}, err
	}
	if err = tx.Commit(); err != nil {
		return Repository{}, err
	}
	return s.GetRepository(ctx, r.ID)
}
func (s *Service) DeleteRepository(ctx context.Context, repoID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM skill_repositories WHERE id=?`, repoID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRepositoryNotFound
	}
	return nil
}

// RecordRepositoryScan rejects results fetched using an obsolete connection or scope.
func (s *Service) RecordRepositoryScan(ctx context.Context, repoID string, revision int, commit string, candidates json.RawMessage, scanError string, errorCode ...string) error {
	var res sql.Result
	var err error
	if scanError != "" {
		code := ""
		if len(errorCode) > 0 {
			code = errorCode[0]
		}
		res, err = s.DB.ExecContext(ctx, `UPDATE skill_repositories SET last_error=?,last_error_code=? WHERE id=? AND revision=?`, scanError, code, repoID, revision)
	} else {
		if !repositoryCommitRE.MatchString(commit) || !json.Valid(candidates) || len(candidates) > 8<<20 {
			return fmt.Errorf("invalid repository scan")
		}
		var list []json.RawMessage
		if json.Unmarshal(candidates, &list) != nil {
			return fmt.Errorf("invalid repository candidates")
		}
		res, err = s.DB.ExecContext(ctx, `UPDATE skill_repositories SET commit_id=?,candidates_json=?,last_scanned_at=?,last_error='',last_error_code='' WHERE id=? AND revision=?`, commit, string(candidates), nowUTC(), repoID, revision)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRepositoryConflict
	}
	return nil
}

// RepositorySource keeps the adopted baseline distinct from a downloaded update.
type RepositorySource struct {
	SkillID          string `json:"skill_id"`
	Name             string `json:"name"`
	RepositoryID     string `json:"repository_id"`
	URL              string `json:"url"`
	Ref              string `json:"ref"`
	Path             string `json:"path"`
	Commit           string `json:"commit"`
	TreeHash         string `json:"tree_hash"`
	BaseVersionID    string `json:"base_version_id"`
	LatestVersionID  string `json:"latest_version_id"`
	CurrentVersionID string `json:"current_version_id"`
	FetchedAt        string `json:"fetched_at"`
	LocalModified    bool   `json:"local_modified"`
	UpdateAvailable  bool   `json:"update_available"`
	SourceRemoved    bool   `json:"source_removed"`
}

const sourceSelect = `SELECT b.skill_id,s.name,COALESCE(b.repository_id,''),b.repository_url,b.ref,b.path,b.commit_id,b.tree_hash,b.base_version_id,b.latest_version_id,COALESCE(p.version_id,''),b.fetched_at FROM skill_repository_bindings b JOIN skills s ON s.id=b.skill_id LEFT JOIN pointers p ON p.entity_type='skill' AND p.entity_id=b.skill_id AND p.name='current'`

func scanRepositorySource(row repositoryScanner) (RepositorySource, error) {
	var b RepositorySource
	err := row.Scan(&b.SkillID, &b.Name, &b.RepositoryID, &b.URL, &b.Ref, &b.Path, &b.Commit, &b.TreeHash, &b.BaseVersionID, &b.LatestVersionID, &b.CurrentVersionID, &b.FetchedAt)
	b.LocalModified = b.CurrentVersionID != b.BaseVersionID
	b.UpdateAvailable = b.LatestVersionID != b.BaseVersionID
	b.SourceRemoved = b.RepositoryID == ""
	return b, err
}
func (s *Service) ListRepositorySources(ctx context.Context, repoID string) ([]RepositorySource, error) {
	q := sourceSelect
	args := []any{}
	if repoID != "" {
		q += ` WHERE b.repository_id=? OR (b.repository_url=(SELECT url FROM skill_repositories WHERE id=?) AND b.ref=(SELECT ref FROM skill_repositories WHERE id=?))`
		args = append(args, repoID, repoID, repoID)
	}
	q += ` ORDER BY s.name`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RepositorySource{}
	for rows.Next() {
		b, e := scanRepositorySource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Service) RepositorySourceForSkill(ctx context.Context, skillID string) (RepositorySource, error) {
	return scanRepositorySource(s.DB.QueryRowContext(ctx, sourceSelect+` WHERE b.skill_id=?`, skillID))
}

type RepositoryInstallInput struct {
	RepositoryID             string
	RepositoryRevision       int
	Path                     string
	Commit                   string
	TreeHash                 string
	Name                     string
	ExpectedCurrentVersionID string
	Files                    map[string][]byte
	Copy                     bool
}
type RepositoryInstallResult struct {
	SkillID          string            `json:"skill_id"`
	Name             string            `json:"name"`
	VersionID        string            `json:"version_id"`
	VersionNo        int               `json:"version_no"`
	CurrentVersionID string            `json:"current_version_id"`
	Created          bool              `json:"created"`
	Applied          bool              `json:"applied"`
	Source           *RepositorySource `json:"source,omitempty"`
}
type RepositoryNameConflict struct {
	Name          string
	SuggestedName string
}

func (e *RepositoryNameConflict) Error() string { return "skill name already exists: " + e.Name }

var repositoryNameRE = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,63}$`)
var repositoryCommitRE = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)

// RepositoryFilesHash ignores display names, like remote directory comparisons.
func RepositoryFilesHash(files map[string][]byte) string {
	hashes := map[string]string{}
	for p, b := range files {
		sum := sha256.Sum256(b)
		hashes[p] = hex.EncodeToString(sum[:])
	}
	h, _, _ := version.HashFileManifest(hashes)
	return h
}
func validateRepositoryFiles(files map[string][]byte) error {
	if len(files) == 0 || len(files) > skillpkg.MaxEntries {
		return fmt.Errorf("too many or no skill files")
	}
	md, ok := files["SKILL.md"]
	if !ok {
		return skillpkg.ErrNoSkillMD
	}
	if _, err := skillpkg.ParseFrontmatter(md); err != nil {
		return err
	}
	total := 0
	seen := map[string]bool{}
	for p, b := range files {
		clean, err := skillpkg.CleanRel(p)
		if err != nil || clean != p {
			return fmt.Errorf("unsafe skill file path")
		}
		for _, part := range strings.Split(p, "/") {
			if strings.EqualFold(part, ".git") {
				return fmt.Errorf("git metadata is not a skill file")
			}
		}
		// Downloads must remain representable on case-insensitive desktop filesystems.
		folded := strings.ToLower(p)
		if seen[folded] {
			return fmt.Errorf("colliding skill paths")
		}
		seen[folded] = true
		total += len(b)
		if len(b) > skillpkg.MaxFileBytes || total > skillpkg.MaxTotalBytes {
			return fmt.Errorf("skill import exceeds size limit")
		}
	}
	return nil
}
func (s *Service) InstallRepositorySkill(ctx context.Context, in RepositoryInstallInput) (RepositoryInstallResult, error) {
	if err := validateRepositoryFiles(in.Files); err != nil {
		return RepositoryInstallResult{}, err
	}
	if !repositoryCommitRE.MatchString(in.Commit) {
		return RepositoryInstallResult{}, fmt.Errorf("invalid repository commit")
	}
	if in.Path == "." {
		in.Path = ""
	}
	if in.Path != "" {
		if p, err := skillpkg.CleanRel(in.Path); err != nil || p != in.Path {
			return RepositoryInstallResult{}, fmt.Errorf("invalid skill directory")
		}
	}
	in.Name = strings.TrimSpace(in.Name)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryInstallResult{}, err
	}
	defer tx.Rollback()
	repo, err := scanRepository(tx.QueryRowContext(ctx, `SELECT `+repositoryColumns+` FROM skill_repositories WHERE id=?`, in.RepositoryID))
	if err != nil {
		return RepositoryInstallResult{}, err
	}
	if in.RepositoryRevision != 0 && in.RepositoryRevision != repo.Revision {
		return RepositoryInstallResult{}, ErrRepositoryConflict
	}
	if repo.Root != "" && in.Path != repo.Root && !strings.HasPrefix(in.Path, repo.Root+"/") {
		return RepositoryInstallResult{}, fmt.Errorf("skill directory outside repository scope")
	}
	var source RepositorySource
	bound := false
	if !in.Copy {
		source, err = scanRepositorySource(tx.QueryRowContext(ctx, sourceSelect+` WHERE b.repository_url=? AND b.ref=? AND b.path=?`, repo.URL, repo.Ref, in.Path))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return RepositoryInstallResult{}, err
		}
		bound = err == nil
	}
	skillID := source.SkillID
	name := source.Name
	current := source.CurrentVersionID
	if bound {
		if current != in.ExpectedCurrentVersionID {
			return RepositoryInstallResult{}, ErrRepositoryConflict
		}
	} else {
		if in.ExpectedCurrentVersionID != "" && !in.Copy {
			return RepositoryInstallResult{}, ErrRepositoryConflict
		}
		name = in.Name
		if name == "" {
			meta, _ := skillpkg.ParseFrontmatter(in.Files["SKILL.md"])
			name = meta.Name
		}
		if name == "" {
			name = path.Base(in.Path)
		}
		if !repositoryNameRE.MatchString(name) {
			return RepositoryInstallResult{}, fmt.Errorf("invalid skill name")
		}
		var exists string
		err = tx.QueryRowContext(ctx, `SELECT id FROM skills WHERE name=?`, name).Scan(&exists)
		if err == nil {
			return RepositoryInstallResult{}, &RepositoryNameConflict{Name: name, SuggestedName: repositorySuggestedName(ctx, tx, name)}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return RepositoryInstallResult{}, err
		}
		skillID = id.New("skill")
		_, err = tx.ExecContext(ctx, `INSERT INTO skills(id,user_id,name,optimize_mode,canary_ratio,canary_runs,trust_level,created_at) VALUES(?,'owner',?,'off',0.2,10,'local',?)`, skillID, name, nowUTC())
		if err != nil {
			return RepositoryInstallResult{}, err
		}
	}
	hashes := map[string]string{}
	for p, b := range in.Files {
		sum := sha256.Sum256(b)
		hashes[p] = hex.EncodeToString(sum[:])
	}
	contentHash, manifest, err := version.HashJSON(Manifest{Name: name, Files: hashes})
	if err != nil {
		return RepositoryInstallResult{}, err
	}
	var vid string
	var vno int
	created := false
	err = tx.QueryRowContext(ctx, `SELECT id,version_no FROM versions WHERE entity_type='skill' AND entity_id=? AND content_hash=?`, skillID, contentHash).Scan(&vid, &vno)
	provenance, _ := json.Marshal(map[string]string{"repository_id": repo.ID, "url": repo.URL, "ref": repo.Ref, "path": in.Path, "commit": in.Commit})
	if errors.Is(err, sql.ErrNoRows) {
		created = true
		vid = id.New("ver")
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM versions WHERE entity_type='skill' AND entity_id=?`, skillID).Scan(&vno); err != nil {
			return RepositoryInstallResult{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO versions(id,user_id,entity_type,entity_id,version_no,content_hash,parent_version_id,content_json,source,source_ref,message,archived,created_at) VALUES(?,'owner','skill',?,?,?,?,?,'repository_import',?,?,0,?)`, vid, skillID, vno, contentHash, repositoryNull(current), string(manifest), string(provenance), "repository import "+name, nowUTC())
		if err != nil {
			return RepositoryInstallResult{}, err
		}
		for p, b := range in.Files {
			h := hashes[p]
			if _, err = tx.ExecContext(ctx, `INSERT INTO blobs(sha256,size,storage,data,refcount) VALUES(?,?,'inline',?,1) ON CONFLICT(sha256) DO UPDATE SET refcount=refcount+1`, h, len(b), b); err != nil {
				return RepositoryInstallResult{}, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO version_files(version_id,path,sha256,mode) VALUES(?,?,?,420)`, vid, p, h); err != nil {
				return RepositoryInstallResult{}, err
			}
		}
		if err = repositoryOp(ctx, tx, skillID, "version_create", "", vid, string(provenance)); err != nil {
			return RepositoryInstallResult{}, err
		}
	} else if err != nil {
		return RepositoryInstallResult{}, err
	}
	if !bound {
		if _, err = tx.ExecContext(ctx, `INSERT INTO pointers(entity_type,entity_id,name,version_id,updated_at) VALUES('skill',?,'current',?,?)`, skillID, vid, nowUTC()); err != nil {
			return RepositoryInstallResult{}, err
		}
		if err = repositoryOp(ctx, tx, skillID, "pointer_move", "", vid, string(provenance)); err != nil {
			return RepositoryInstallResult{}, err
		}
	}
	if !in.Copy {
		if bound {
			_, err = tx.ExecContext(ctx, `UPDATE skill_repository_bindings SET repository_id=?,commit_id=?,tree_hash=?,latest_version_id=?,fetched_at=? WHERE skill_id=?`, repo.ID, in.Commit, in.TreeHash, vid, nowUTC(), skillID)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO skill_repository_bindings(skill_id,repository_id,repository_url,ref,path,commit_id,tree_hash,base_version_id,latest_version_id,fetched_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, skillID, repo.ID, repo.URL, repo.Ref, in.Path, in.Commit, in.TreeHash, vid, vid, nowUTC())
		}
		if err != nil {
			return RepositoryInstallResult{}, err
		}
	}
	if err = repositoryOp(ctx, tx, skillID, "repository_fetch", current, vid, string(provenance)); err != nil {
		return RepositoryInstallResult{}, err
	}
	out := RepositoryInstallResult{SkillID: skillID, Name: name, VersionID: vid, VersionNo: vno, Created: created, Applied: !bound || current == vid && source.BaseVersionID == vid, CurrentVersionID: current}
	if !bound {
		out.CurrentVersionID = vid
	}
	if !in.Copy {
		b, e := scanRepositorySource(tx.QueryRowContext(ctx, sourceSelect+` WHERE b.skill_id=?`, skillID))
		if e != nil {
			return out, e
		}
		out.Source = &b
	}
	if err = tx.Commit(); err != nil {
		return RepositoryInstallResult{}, err
	}
	return out, nil
}
func repositoryNull(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func repositorySuggestedName(ctx context.Context, tx *sql.Tx, name string) string {
	runes := []rune(name)
	if len(runes) > 54 {
		runes = runes[:54]
	}
	for i := 2; i < 10000; i++ {
		candidate := fmt.Sprintf("%s-%d", string(runes), i)
		var n int
		if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE name=?`, candidate).Scan(&n) == nil && n == 0 {
			return candidate
		}
	}
	return string(runes) + "-copy"
}
func repositoryOp(ctx context.Context, tx *sql.Tx, skillID, action, from, to, detail string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO operation_log(user_id,at,entity_type,entity_id,action,pointer,from_version_id,to_version_id,actor,reason,detail_json) VALUES('owner',?,'skill',?,?,?, ?,?,'user','skill repository',?)`, nowUTC(), skillID, action, func() string {
		if action == "pointer_move" {
			return "current"
		}
		return ""
	}(), repositoryNull(from), repositoryNull(to), detail)
	return err
}

// ApplyRepositoryVersion is an explicit compare-and-swap, including locally modified skills.
func (s *Service) ApplyRepositoryVersion(ctx context.Context, skillID, versionID, expectedCurrentVersionID string) error {
	if expectedCurrentVersionID == "" {
		return ErrRepositoryConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := scanRepositorySource(tx.QueryRowContext(ctx, sourceSelect+` WHERE b.skill_id=?`, skillID))
	if err != nil {
		return err
	}
	if b.CurrentVersionID != expectedCurrentVersionID || b.LatestVersionID != versionID {
		return ErrRepositoryConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE pointers SET version_id=?,updated_at=? WHERE entity_type='skill' AND entity_id=? AND name='current' AND version_id=?`, versionID, nowUTC(), skillID, expectedCurrentVersionID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrRepositoryConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE skill_repository_bindings SET base_version_id=? WHERE skill_id=?`, versionID, skillID); err != nil {
		return err
	}
	detail, _ := json.Marshal(b)
	if err = repositoryOp(ctx, tx, skillID, "pointer_move", expectedCurrentVersionID, versionID, string(detail)); err != nil {
		return err
	}
	return tx.Commit()
}
