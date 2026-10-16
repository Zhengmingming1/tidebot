// Package skillstore imports SKILL.md folders as content-hashed immutable skill versions.
package skillstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Manifest stored in versions.content_json for skills.
type Manifest struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"` // rel path -> sha256
}

// Service manages skill entities.
type Service struct {
	DB      *sql.DB
	Ver     *version.Service
	Tasks   *tasksvc.Service
	BlobDir string // optional file storage; empty = inline blobs table
}

func New(db *sql.DB, ver *version.Service, tasks *tasksvc.Service, blobDir string) *Service {
	return &Service{DB: db, Ver: ver, Tasks: tasks, BlobDir: blobDir}
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// SkillView is skill metadata + current version.
type SkillView struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	CurrentVersion *version.Version `json:"current_version,omitempty"`
	Manifest       *Manifest        `json:"manifest,omitempty"`
}

// ImportDir imports a directory containing SKILL.md (or any files) as a new skill version.
func (s *Service) ImportDir(ctx context.Context, name, dir, actor string) (SkillView, error) {
	if name == "" {
		name = filepath.Base(dir)
	}
	if actor == "" {
		actor = "user"
	}
	files, err := readTree(dir)
	if err != nil {
		return SkillView{}, err
	}
	if _, ok := files["SKILL.md"]; !ok {
		// allow lowercase
		if _, ok2 := files["skill.md"]; !ok2 {
			return SkillView{}, fmt.Errorf("directory must contain SKILL.md")
		}
	}
	skillID, err := s.ensureSkill(ctx, name)
	if err != nil {
		return SkillView{}, err
	}
	ver, man, err := s.CommitFiles(ctx, skillID, name, files, "import", actor, false)
	if err != nil {
		return SkillView{}, err
	}
	return SkillView{ID: skillID, Name: name, CurrentVersion: ver, Manifest: &man}, nil
}

// CommitFiles stores files as a skill version. noMove leaves `current` alone (proposals).
func (s *Service) CommitFiles(ctx context.Context, skillID, name string, files map[string][]byte, source, actor string, noMove bool) (*version.Version, Manifest, error) {
	hashes := map[string]string{}
	for path, data := range files {
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		hashes[path] = h
		if err := s.putBlob(ctx, h, data); err != nil {
			return nil, Manifest{}, err
		}
	}
	man := Manifest{Name: name, Files: hashes}
	res, err := s.Ver.Commit(ctx, version.CommitInput{
		EntityType: version.EntitySkill, EntityID: skillID, NoMove: noMove,
		Content: man, Source: source, Actor: actor, Reason: source + " skill", Message: source + " " + name,
	})
	if err != nil {
		return nil, Manifest{}, err
	}
	for path, h := range hashes {
		_, _ = s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO version_files(version_id, path, sha256, mode) VALUES(?,?,?,420)`,
			res.Version.ID, path, h)
	}
	return res.Version, man, nil
}

// Files returns the files of a skill version.
func (s *Service) Files(ctx context.Context, versionID string) (map[string][]byte, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, sha256 FROM version_files WHERE version_id=?`, versionID)
	if err != nil {
		return nil, err
	}
	type vf struct{ p, h string }
	var list []vf
	for rows.Next() {
		var f vf
		if err := rows.Scan(&f.p, &f.h); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, f)
	}
	rows.Close()
	out := map[string][]byte{}
	for _, f := range list {
		b, err := s.getBlob(ctx, f.h)
		if err != nil {
			return nil, err
		}
		out[f.p] = b
	}
	return out, nil
}

func (s *Service) ensureSkill(ctx context.Context, name string) (string, error) {
	var id0 string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM skills WHERE name=?`, name).Scan(&id0)
	if err == nil {
		return id0, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	sid := id.New("skill")
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO skills(id, user_id, name, optimize_mode, canary_ratio, canary_runs, trust_level, created_at)
VALUES(?,?,?,?,0.2,10,'local',?)`, sid, "owner", name, "off", nowUTC())
	return sid, err
}

func (s *Service) putBlob(ctx context.Context, sha string, data []byte) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO blobs(sha256, size, storage, data, refcount) VALUES(?,?, 'inline', ?, 1)
ON CONFLICT(sha256) DO UPDATE SET refcount=refcount+1`, sha, len(data), data)
	return err
}

func readTree(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".git/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	return out, err
}

// List returns all skills.
func (s *Service) List(ctx context.Context) ([]SkillView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	type pair struct{ ID, Name string }
	var base []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			rows.Close()
			return nil, err
		}
		base = append(base, p)
	}
	rows.Close()
	var out []SkillView
	for _, p := range base {
		v := SkillView{ID: p.ID, Name: p.Name}
		if ver, err := s.Ver.CurrentVersion(ctx, version.EntitySkill, v.ID); err == nil {
			v.CurrentVersion = ver
			var m Manifest
			if json.Unmarshal([]byte(ver.ContentJSON), &m) == nil {
				v.Manifest = &m
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Rollback moves skill current pointer.
func (s *Service) Rollback(ctx context.Context, skillID, versionID, actor string) error {
	return s.Ver.MovePointer(ctx, version.EntitySkill, skillID, version.PointerCurrent, versionID, actor, "rollback", "")
}

// PinOnTask sets a pinned skill binding on the task's current spec (creates new task version).
func (s *Service) PinOnTask(ctx context.Context, taskID, skillID, skillVersionID, actor string) error {
	view, err := s.Tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	spec := tasksvc.Spec{Prompt: view.Task.Prompt}
	if view.Spec != nil {
		spec = *view.Spec
	}
	name := skillID
	_ = s.DB.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, skillID).Scan(&name)
	found := false
	for i := range spec.SkillBindings {
		if spec.SkillBindings[i].SkillID == skillID {
			spec.SkillBindings[i].Mode = "pinned"
			spec.SkillBindings[i].VersionID = skillVersionID
			spec.SkillBindings[i].SkillName = name
			found = true
			break
		}
	}
	if !found {
		spec.SkillBindings = append(spec.SkillBindings, tasksvc.SkillBinding{
			SkillID: skillID, SkillName: name, Mode: "pinned", VersionID: skillVersionID,
		})
	}
	_, err = s.Tasks.UpdateSpec(ctx, taskID, spec, actor, "pin skill "+skillID)
	return err
}

// Materialize writes skill files for bindings into workspace/.claude/skills/<name>/.
func (s *Service) Materialize(ctx context.Context, workspace string, bindings []tasksvc.SkillBinding) error {
	base := filepath.Join(workspace, ".claude", "skills")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	for _, b := range bindings {
		verID := b.VersionID
		if b.Mode == "follow" || verID == "" {
			cur, err := s.Ver.CurrentVersion(ctx, version.EntitySkill, b.SkillID)
			if err != nil {
				return fmt.Errorf("skill %s current: %w", b.SkillID, err)
			}
			verID = cur.ID
		}
		ver, err := s.Ver.GetVersion(ctx, verID)
		if err != nil {
			return err
		}
		var man Manifest
		if err := json.Unmarshal([]byte(ver.ContentJSON), &man); err != nil {
			return err
		}
		dirName := man.Name
		if dirName == "" {
			dirName = b.SkillName
		}
		if dirName == "" {
			dirName = b.SkillID
		}
		destRoot := filepath.Join(base, dirName)
		if err := os.MkdirAll(destRoot, 0o700); err != nil {
			return err
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT path, sha256 FROM version_files WHERE version_id=?`, verID)
		if err != nil {
			return err
		}
		type vf struct{ Path, SHA string }
		var files []vf
		for rows.Next() {
			var f vf
			if err := rows.Scan(&f.Path, &f.SHA); err != nil {
				rows.Close()
				return err
			}
			files = append(files, f)
		}
		rows.Close()
		if len(files) == 0 {
			// fall back to manifest keys + blobs by hash only
			for path, sha := range man.Files {
				files = append(files, vf{Path: path, SHA: sha})
			}
		}
		for _, f := range files {
			data, err := s.getBlob(ctx, f.SHA)
			if err != nil {
				return err
			}
			dest := filepath.Join(destRoot, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(dest, data, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) getBlob(ctx context.Context, sha string) ([]byte, error) {
	var data []byte
	err := s.DB.QueryRowContext(ctx, `SELECT data FROM blobs WHERE sha256=?`, sha).Scan(&data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Copy for tests / completeness
var _ = io.EOF
