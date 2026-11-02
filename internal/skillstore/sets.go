package skillstore

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

const SkillSetEntity = "skill_set"
const skillSetFormat = "tidebot-skill-set/1"

type PartialSetImportError struct {
	ImportedSkillIDs []string
	Err              error
}

func (e *PartialSetImportError) Error() string {
	return fmt.Sprintf("skill set was not created; member skills already written: %s; %v", strings.Join(e.ImportedSkillIDs, ", "), e.Err)
}
func (e *PartialSetImportError) Unwrap() error { return e.Err }

var ErrSetConflict = errors.New("skill set changed; reload before saving")

// A single core owns the database. Serialize pointer comparisons and commits together.
var setWrites sync.Mutex

type SkillSet struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	VersionID     string                 `json:"version_id"`
	VersionNo     int                    `json:"version_no"`
	SkillBindings []tasksvc.SkillBinding `json:"skill_bindings"`
	Details       SkillSetDetails        `json:"details"`
}

type setContent struct {
	Name          string                 `json:"name"`
	SkillBindings []tasksvc.SkillBinding `json:"skill_bindings"`
	Details       SkillSetDetails        `json:"details"`
}

func (s *Service) GetSet(ctx context.Context, setID string) (SkillSet, error) {
	var out SkillSet
	if err := s.DB.QueryRowContext(ctx, `SELECT id,name FROM skill_sets WHERE id=? AND deleted=0`, setID).Scan(&out.ID, &out.Name); err != nil {
		return out, err
	}
	v, err := s.Ver.CurrentVersion(ctx, SkillSetEntity, setID)
	if err != nil {
		return out, err
	}
	var c setContent
	if err = json.Unmarshal([]byte(v.ContentJSON), &c); err != nil {
		return out, err
	}
	out.Name = c.Name
	out.SkillBindings = c.SkillBindings
	out.Details = c.Details
	out.VersionID = v.ID
	out.VersionNo = v.VersionNo
	return out, nil
}

func (s *Service) ListSets(ctx context.Context) ([]SkillSet, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM skill_sets WHERE deleted=0 ORDER BY updated_at DESC,id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var i string
		if err = rows.Scan(&i); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []SkillSet{}
	for _, i := range ids {
		v, err := s.GetSet(ctx, i)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) SaveSet(ctx context.Context, setID, name, expected string, bindings []tasksvc.SkillBinding) (SkillSet, error) {
	return s.SaveSetWithDetails(ctx, setID, name, expected, bindings, nil)
}

func (s *Service) SaveSetWithDetails(ctx context.Context, setID, name, expected string, bindings []tasksvc.SkillBinding, details *SkillSetDetails) (SkillSet, error) {
	if details != nil {
		if err := ValidateSkillSetDetails(*details); err != nil {
			return SkillSet{}, err
		}
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return SkillSet{}, errors.New("skill set name is required (max 80 characters)")
	}
	if len(bindings) < 2 {
		return SkillSet{}, errors.New("a skill set requires at least two skills")
	}
	resolved, err := s.ResolveBindings(ctx, bindings)
	if err != nil {
		return SkillSet{}, err
	}
	if err := s.ValidateDependencyBindings(ctx, resolved); err != nil {
		return SkillSet{}, err
	}
	// Check command collisions before accepting a composition, without writing a stage.
	names := map[string]bool{}
	for _, b := range resolved {
		files, man, err := s.versionFiles(ctx, b.VersionID)
		if err != nil {
			return SkillSet{}, err
		}
		if len(files["SKILL.md"]) == 0 {
			return SkillSet{}, fmt.Errorf("skill %s has no SKILL.md", b.SkillID)
		}
		name := stagedName(files, man, b, 0)
		if _, err := stagedSkillMD(files["SKILL.md"], name); err != nil {
			return SkillSet{}, err
		}
		if names[name] {
			return SkillSet{}, fmt.Errorf("duplicate skill command name %q", name)
		}
		names[name] = true
	}
	setWrites.Lock()
	defer setWrites.Unlock()
	metadata := SkillSetDetails{}
	if details != nil {
		metadata = *details
	}
	fresh := setID == ""
	if !fresh {
		cur, err := s.GetSet(ctx, setID)
		if err != nil {
			return SkillSet{}, err
		}
		if details == nil {
			metadata = cur.Details
		}
		if expected == "" || cur.VersionID != expected {
			return SkillSet{}, ErrSetConflict
		}
	}
	if fresh {
		setID = id.New("set")
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO skill_sets(id,name,created_at,updated_at) VALUES(?,?,?,?)`, setID, name, nowUTC(), nowUTC()); err != nil {
			return SkillSet{}, err
		}
	}
	_, err = s.Ver.Commit(ctx, version.CommitInput{EntityType: SkillSetEntity, EntityID: setID, Content: setContent{Name: name, SkillBindings: resolved, Details: metadata}, Source: "user_edit", Actor: "user", Reason: "save skill set"})
	if err != nil {
		if fresh {
			_, _ = s.DB.ExecContext(ctx, `DELETE FROM skill_sets WHERE id=?`, setID)
		}
		return SkillSet{}, err
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE skill_sets SET name=?,updated_at=? WHERE id=?`, name, nowUTC(), setID); err != nil {
		return SkillSet{}, err
	}
	return s.GetSet(ctx, setID)
}

func (s *Service) DeleteSet(ctx context.Context, setID string) error {
	setWrites.Lock()
	defer setWrites.Unlock()
	res, err := s.DB.ExecContext(ctx, `UPDATE skill_sets SET deleted=1,updated_at=? WHERE id=? AND deleted=0`, nowUTC(), setID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type setArchive struct {
	Format  string                 `json:"format"`
	Name    string                 `json:"name"`
	Members []tasksvc.SkillBinding `json:"members"`
	Details SkillSetDetails        `json:"details"`
}

func (s *Service) ExportSet(ctx context.Context, setID string) ([]byte, error) {
	set, err := s.GetSet(ctx, setID)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest := setArchive{Format: skillSetFormat, Name: set.Name, Members: set.SkillBindings, Details: set.Details}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	w, err := zw.Create("skill-set.json")
	if err != nil {
		return nil, err
	}
	if _, err = w.Write(raw); err != nil {
		return nil, err
	}
	total := len(raw)
	count := 1
	for _, b := range set.SkillBindings {
		files, _, err := s.versionFiles(ctx, b.VersionID)
		if err != nil {
			return nil, err
		}
		for p, data := range files {
			rel, err := skillpkg.CleanRel(p)
			if err != nil {
				return nil, err
			}
			total += len(data)
			count++
			if len(data) > skillpkg.MaxFileBytes || total > skillpkg.MaxTotalBytes || count > skillpkg.MaxEntries {
				return nil, errors.New("skill set is too large to export")
			}
			w, err := zw.Create("skills/" + b.SkillID + "/" + rel)
			if err != nil {
				return nil, err
			}
			if _, err = w.Write(data); err != nil {
				return nil, err
			}
		}
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > skillpkg.MaxArchiveBytes {
		return nil, errors.New("skill set archive exceeds 20 MB")
	}
	return buf.Bytes(), nil
}

// ImportSet creates a new composition. Existing skills are reused only for identical
// content; a name collision creates a new named skill, never advances an existing one.
func (s *Service) ImportSet(ctx context.Context, data []byte) (SkillSet, error) {
	dir, err := os.MkdirTemp("", "tidebot-set-")
	if err != nil {
		return SkillSet{}, err
	}
	defer os.RemoveAll(dir)
	if err = skillpkg.Extract("set.zip", data, dir); err != nil {
		return SkillSet{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "skill-set.json"))
	if err != nil {
		return SkillSet{}, errors.New("skill-set.json is missing")
	}
	var man setArchive
	if json.Unmarshal(raw, &man) != nil || man.Format != skillSetFormat {
		return SkillSet{}, errors.New("unsupported skill set archive")
	}
	members, err := tasksvc.NormalizeBindings(man.Members)
	if err != nil || len(members) < 2 {
		return SkillSet{}, errors.New("invalid skill set members")
	}
	if strings.TrimSpace(man.Name) == "" || utf8.RuneCountInString(man.Name) > 80 {
		return SkillSet{}, errors.New("invalid skill set name")
	}
	if err := ValidateSkillSetDetails(man.Details); err != nil {
		return SkillSet{}, err
	}
	// Validate every member before touching the store.
	trees := make([]map[string][]byte, len(members))
	commandNames := map[string]bool{}
	for i, b := range members {
		tree, err := readTree(filepath.Join(dir, "skills", b.SkillID))
		if err != nil {
			return SkillSet{}, err
		}
		if raw, ok := tree["tidebot.json"]; ok {
			if _, err := ValidateSkillManifest(raw); err != nil {
				return SkillSet{}, err
			}
		}
		md, ok := tree["SKILL.md"]
		if !ok {
			return SkillSet{}, fmt.Errorf("skill %s has no SKILL.md", b.SkillID)
		}
		if _, err = skillpkg.ParseFrontmatter(md); err != nil {
			return SkillSet{}, err
		}
		name := stagedName(tree, Manifest{Name: b.SkillName}, b, i)
		if _, err := stagedSkillMD(md, name); err != nil {
			return SkillSet{}, err
		}
		if commandNames[name] {
			return SkillSet{}, fmt.Errorf("duplicate skill command name %q", name)
		}
		commandNames[name] = true
		trees[i] = tree
	}
	all, err := s.List(ctx)
	if err != nil {
		return SkillSet{}, err
	}
	var created []string
	partial := func(err error) (SkillSet, error) {
		if len(created) > 0 {
			return SkillSet{}, &PartialSetImportError{ImportedSkillIDs: append([]string(nil), created...), Err: err}
		}
		return SkillSet{}, err
	}
	for i := range members {
		b := &members[i]
		matched := false
		for _, sk := range all {
			if sk.CurrentVersion != nil {
				existing, err := s.Files(ctx, sk.CurrentVersion.ID)
				if err != nil {
					return partial(err)
				}
				if reflect.DeepEqual(existing, trees[i]) {
					b.SkillID = sk.ID
					b.VersionID = sk.CurrentVersion.ID
					b.Mode = "pinned"
					matched = true
					break
				}
			}
		}
		if matched {
			continue
		}
		name := strings.TrimSpace(b.SkillName)
		if name == "" {
			name = b.SkillID
		}
		r := []rune(name)
		if len(r) > 45 {
			name = string(r[:45])
		}
		for _, sk := range all {
			if sk.Name == name {
				name += " imported " + strings.TrimPrefix(id.New("s"), "s_")[:8]
				break
			}
		}
		sv, err := s.createSetMember(ctx, name, trees[i])
		if sv.ID != "" {
			created = append(created, sv.ID)
		}
		if err != nil {
			return partial(err)
		}
		b.SkillID = sv.ID
		b.SkillName = sv.Name
		b.VersionID = sv.CurrentVersion.ID
		b.Mode = "pinned"
		all = append(all, sv)
	}
	set, err := s.SaveSetWithDetails(ctx, "", man.Name, "", members, &man.Details)
	if err != nil {
		return partial(err)
	}
	return set, nil
}

func (s *Service) RecordRunBindings(ctx context.Context, runID string, bindings []tasksvc.SkillBinding) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, b := range bindings {
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_skill_bindings(run_id,skill_id,version_id,skill_name,role,position) VALUES(?,?,?,?,?,?)`, runID, b.SkillID, b.VersionID, b.SkillName, b.Role, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) RunBindings(ctx context.Context, runID string) ([]tasksvc.SkillBinding, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT skill_id,version_id,skill_name,role FROM run_skill_bindings WHERE run_id=? ORDER BY position`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tasksvc.SkillBinding{}
	for rows.Next() {
		var b tasksvc.SkillBinding
		b.Mode = "pinned"
		if err = rows.Scan(&b.SkillID, &b.VersionID, &b.SkillName, &b.Role); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Insert, rather than ensureSkill, so a racing import can never move an existing
// skill's current pointer. Reusing existing content is handled before this call.
func (s *Service) createSetMember(ctx context.Context, name string, files map[string][]byte) (SkillView, error) {
	skillID := id.New("skill")
	for attempt := 0; attempt < 3; attempt++ {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO skills(id,name,created_at) VALUES(?,?,?)`, skillID, name, nowUTC())
		if err == nil {
			v, man, err := s.CommitFiles(ctx, skillID, name, files, "skill_set_import", "user", false)
			return SkillView{ID: skillID, Name: name, CurrentVersion: v, Manifest: &man}, err
		}
		var exists int
		if s.DB.QueryRowContext(ctx, `SELECT count(*) FROM skills WHERE name=?`, name).Scan(&exists) != nil || exists == 0 {
			return SkillView{}, err
		}
		r := []rune(name)
		if len(r) > 40 {
			name = string(r[:40])
		}
		name += " imported " + strings.TrimPrefix(id.New("s"), "s_")[:8]
	}
	return SkillView{}, errors.New("cannot allocate an imported skill name")
}
