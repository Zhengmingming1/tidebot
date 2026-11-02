package skillstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

const SkillManifestFormat = "tidebot-skill/1"
const MaxSkillManifestBytes = 64 << 10

// SkillManifest is Tidebot-specific metadata beside, never inside, SKILL.md.
type SkillManifest struct {
	Format       string               `json:"format"`
	Dependencies []SkillDependency    `json:"dependencies,omitempty"`
	Environment  []RuntimeRequirement `json:"environment,omitempty"`
}
type SkillDependency struct {
	SkillName     string `json:"skill_name"`
	Optional      bool   `json:"optional,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}
type RuntimeRequirement struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Optional bool   `json:"optional,omitempty"`
	Version  string `json:"version,omitempty"`
}

var manifestHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var executableName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)

func ValidateSkillManifest(raw []byte) (SkillManifest, error) {
	var m SkillManifest
	if len(raw) > MaxSkillManifestBytes {
		return m, errors.New("tidebot.json exceeds 64 KiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("invalid tidebot.json: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return m, errors.New("tidebot.json must contain one JSON object")
	}
	if m.Format != SkillManifestFormat {
		return m, fmt.Errorf("tidebot.json format must be %q", SkillManifestFormat)
	}
	if len(m.Dependencies) > 16 || len(m.Environment) > 32 {
		return m, errors.New("tidebot.json allows at most 16 skills and 32 environment requirements")
	}
	seen := map[string]bool{}
	for i := range m.Dependencies {
		d := &m.Dependencies[i]
		d.SkillName = strings.TrimSpace(d.SkillName)
		if d.SkillName == "" || len(d.SkillName) > 256 || strings.ContainsAny(d.SkillName, "\x00\r\n") {
			return m, errors.New("dependency skill_name is required (max 256 bytes)")
		}
		if seen[d.SkillName] {
			return m, fmt.Errorf("duplicate dependency %q", d.SkillName)
		}
		seen[d.SkillName] = true
		if d.ContentSHA256 != "" && !manifestHash.MatchString(d.ContentSHA256) {
			return m, errors.New("content_sha256 must be 64 lowercase hexadecimal characters")
		}
	}
	seen = map[string]bool{}
	for i := range m.Environment {
		r := &m.Environment[i]
		r.Name = strings.TrimSpace(r.Name)
		r.Version = strings.TrimSpace(r.Version)
		if r.Name == "" || len(r.Name) > 256 || strings.ContainsAny(r.Name+r.Version, "\x00\r\n") || len(r.Version) > 128 {
			return m, errors.New("invalid environment requirement name or version")
		}
		switch r.Kind {
		case "command", "python_package":
			if !executableName.MatchString(r.Name) {
				return m, fmt.Errorf("%s must be a name, without paths or arguments", r.Kind)
			}
		case "env":
			if !environmentName.MatchString(r.Name) {
				return m, errors.New("invalid environment variable name")
			}
		case "file":
			clean, err := skillpkg.CleanRel(r.Name)
			if err != nil || clean != r.Name {
				return m, errors.New("file requirement must be a safe path relative to the skill")
			}
		case "tool", "mcp":
		default:
			return m, fmt.Errorf("unknown environment requirement kind %q", r.Kind)
		}
		key := r.Kind + "\x00" + r.Name
		if seen[key] {
			return m, fmt.Errorf("duplicate environment requirement %s %q", r.Kind, r.Name)
		}
		seen[key] = true
	}
	return m, nil
}

func (s *Service) ReadSkillManifest(ctx context.Context, versionID string) (SkillManifest, error) {
	v, err := s.Ver.GetVersion(ctx, versionID)
	if err != nil {
		return SkillManifest{}, err
	}
	if v.EntityType != version.EntitySkill {
		return SkillManifest{}, errors.New("manifest version is not a skill")
	}
	raw, err := s.manifestFile(ctx, versionID, "tidebot.json", MaxSkillManifestBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return SkillManifest{}, nil
	}
	if err != nil {
		return SkillManifest{}, err
	}
	return ValidateSkillManifest(raw)
}

// Read one bounded blob; dependency checking never loads unrelated skill files.
func (s *Service) manifestFile(ctx context.Context, versionID, path string, limit int) ([]byte, error) {
	var size int
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT vf.sha256,b.size FROM version_files vf JOIN blobs b ON b.sha256=vf.sha256 WHERE vf.version_id=? AND vf.path=?`, versionID, path).Scan(&hash, &size)
	if err != nil {
		return nil, err
	}
	if size > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	data, err := s.getBlob(ctx, hash)
	if err == nil && len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return data, err
}

// File-tree hashes exclude the library display name, so constraints survive ZIP import.
func (s *Service) SkillContentSHA256(ctx context.Context, versionID string) (string, error) {
	v, err := s.Ver.GetVersion(ctx, versionID)
	if err != nil {
		return "", err
	}
	if v.EntityType != version.EntitySkill {
		return "", errors.New("not a skill version")
	}
	var man Manifest
	if err = json.Unmarshal([]byte(v.ContentJSON), &man); err != nil {
		return "", err
	}
	raw, err := json.Marshal(man.Files)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type SkillManifestView struct {
	SkillID       string        `json:"skill_id"`
	VersionID     string        `json:"version_id"`
	Manifest      SkillManifest `json:"manifest"`
	ContentSHA256 string        `json:"content_sha256"`
	CanonicalName string        `json:"canonical_name"`
}

var ErrManifestConflict = errors.New("skill changed; reload before saving its manifest")

func (s *Service) GetSkillManifest(ctx context.Context, skillID, versionID string) (SkillManifestView, error) {
	mode := "pinned"
	if versionID == "" {
		mode = "follow"
	}
	vid, err := s.ResolveVersion(ctx, tasksvc.SkillBinding{SkillID: skillID, Mode: mode, VersionID: versionID})
	if err != nil {
		return SkillManifestView{}, err
	}
	m, err := s.ReadSkillManifest(ctx, vid)
	if err != nil {
		return SkillManifestView{}, err
	}
	if m.Format == "" {
		m.Format = SkillManifestFormat
	}
	hash, err := s.SkillContentSHA256(ctx, vid)
	var canonical string
	if err == nil {
		err = s.DB.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, skillID).Scan(&canonical)
	}
	if md, readErr := s.manifestFile(ctx, vid, "SKILL.md", 64<<10); readErr == nil {
		if meta, parseErr := skillpkg.ParseFrontmatter(md); parseErr == nil && meta.Name != "" {
			canonical = meta.Name
		}
	}
	return SkillManifestView{SkillID: skillID, VersionID: vid, Manifest: m, ContentSHA256: hash, CanonicalName: canonical}, err
}

// SaveSkillManifest creates an immutable version and only moves the expected pointer.
func (s *Service) SaveSkillManifest(ctx context.Context, skillID, expected string, m SkillManifest) (SkillManifestView, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return SkillManifestView{}, err
	}
	m, err = ValidateSkillManifest(raw)
	if err != nil {
		return SkillManifestView{}, err
	}
	current, err := s.ResolveVersion(ctx, tasksvc.SkillBinding{SkillID: skillID, Mode: "follow"})
	if err != nil {
		return SkillManifestView{}, err
	}
	if expected == "" || expected != current {
		return SkillManifestView{}, ErrManifestConflict
	}
	files, man, err := s.versionFiles(ctx, current)
	if err != nil {
		return SkillManifestView{}, err
	}
	raw, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return SkillManifestView{}, err
	}
	files["tidebot.json"] = append(raw, '\n')
	v, _, err := s.CommitFiles(ctx, skillID, man.Name, files, "manifest_edit", "user", true)
	if err != nil {
		return SkillManifestView{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SkillManifestView{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE pointers SET version_id=?,updated_at=? WHERE entity_type=? AND entity_id=? AND name=? AND version_id=?`, v.ID, nowUTC(), version.EntitySkill, skillID, version.PointerCurrent, expected)
	if err != nil {
		return SkillManifestView{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return SkillManifestView{}, err
	}
	if n != 1 {
		return SkillManifestView{}, ErrManifestConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_log(user_id,at,entity_type,entity_id,action,pointer,from_version_id,to_version_id,actor,reason) VALUES('owner',?,?,?,?,?,?,?,?,?)`, nowUTC(), version.EntitySkill, skillID, "pointer_move", version.PointerCurrent, expected, v.ID, "user", "edit dependency manifest")
	if err != nil {
		return SkillManifestView{}, err
	}
	if err = tx.Commit(); err != nil {
		return SkillManifestView{}, err
	}
	return s.GetSkillManifest(ctx, skillID, v.ID)
}
