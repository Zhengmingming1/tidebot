package skillstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

type DependencyIssue struct {
	Code      string `json:"code"`
	SkillName string `json:"skill_name"`
	From      string `json:"from,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Message   string `json:"message"`
}
type ManifestRequirement struct {
	SkillName string `json:"skill_name"`
	SkillID   string `json:"skill_id"`
	RuntimeRequirement
}
type DependencyPreview struct {
	Bindings     []tasksvc.SkillBinding `json:"bindings"`
	Additions    []tasksvc.SkillBinding `json:"additions"`
	Issues       []DependencyIssue      `json:"issues"`
	Requirements []ManifestRequirement  `json:"requirements"`
	Resolvable   bool                   `json:"resolvable"`
	Ready        bool                   `json:"ready"`
}
type DependencyResolutionError struct{ Preview DependencyPreview }

func (e *DependencyResolutionError) Error() string {
	if !e.Preview.Resolvable {
		return "skill dependencies need attention before use"
	}
	return "required skill dependencies must be reviewed and added before use"
}

// ValidateDependencyBindings never expands bindings or fetches external dependencies.
func (s *Service) ValidateDependencyBindings(ctx context.Context, bs []tasksvc.SkillBinding) error {
	p, err := s.PreviewDependencies(ctx, bs)
	if err != nil {
		return err
	}
	if !p.Ready {
		return &DependencyResolutionError{Preview: p}
	}
	return nil
}

// PreviewDependencies resolves only installed skills. Additions are proposals for
// the caller to explicitly adopt; optional dependencies remain suggestions.
func (s *Service) PreviewDependencies(ctx context.Context, bs []tasksvc.SkillBinding) (DependencyPreview, error) {
	p := DependencyPreview{Bindings: []tasksvc.SkillBinding{}, Additions: []tasksvc.SkillBinding{}, Issues: []DependencyIssue{}, Requirements: []ManifestRequirement{}, Resolvable: true}
	resolved, err := s.ResolveBindings(ctx, bs)
	if err != nil {
		return p, err
	}
	p.Bindings = append(p.Bindings, resolved...)
	// Names from the exact selected version take precedence over a newer library version.
	aliasCache := map[string][]string{}
	aliases := func(b tasksvc.SkillBinding) []string {
		key := b.SkillID + "/" + b.VersionID
		if a, ok := aliasCache[key]; ok {
			return a
		}
		names := []string{b.SkillName}
		if md, err := s.manifestFile(ctx, b.VersionID, "SKILL.md", 64<<10); err == nil {
			if meta, err := skillpkg.ParseFrontmatter(md); err == nil && meta.Name != "" && meta.Name != b.SkillName {
				names = append(names, meta.Name)
			}
		}
		aliasCache[key] = names
		return names
	}
	// Index names once per preview; do not scan the entire library for every
	// dependency edge or load unrelated version content on the common no-deps path.
	boundNames := map[string][]tasksvc.SkillBinding{}
	addNames := func(index map[string][]tasksvc.SkillBinding, b tasksvc.SkillBinding) {
		for _, name := range aliases(b) {
			index[name] = append(index[name], b)
		}
	}
	for _, b := range p.Bindings {
		addNames(boundNames, b)
	}
	var libraryNames map[string][]tasksvc.SkillBinding
	loadLibrary := func() error {
		if libraryNames != nil {
			return nil
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT s.id,s.name,v.id FROM skills s
JOIN pointers p ON p.entity_type='skill' AND p.entity_id=s.id AND p.name='current'
JOIN versions v ON v.id=p.version_id AND v.entity_type='skill' AND v.entity_id=s.id ORDER BY s.name,s.id`)
		if err != nil {
			return err
		}
		var bs []tasksvc.SkillBinding
		for rows.Next() {
			b := tasksvc.SkillBinding{Mode: "pinned", Role: "auxiliary"}
			if err := rows.Scan(&b.SkillID, &b.SkillName, &b.VersionID); err != nil {
				rows.Close()
				return err
			}
			bs = append(bs, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		libraryNames = map[string][]tasksvc.SkillBinding{}
		for _, b := range bs {
			addNames(libraryNames, b)
		}
		return nil
	}
	hashes := map[string]string{}
	contentHash := func(vid string) (string, error) {
		if hash, ok := hashes[vid]; ok {
			return hash, nil
		}
		hash, err := s.SkillContentSHA256(ctx, vid)
		if err == nil {
			hashes[vid] = hash
		}
		return hash, err
	}
	issue := func(code, name, from string, optional bool, message string) {
		p.Issues = append(p.Issues, DependencyIssue{Code: code, SkillName: name, From: from, Optional: optional, Message: message})
		if !optional {
			p.Resolvable = false
		}
	}
	states := map[string]int{}
	var walk func(tasksvc.SkillBinding, []string) error
	walk = func(b tasksvc.SkillBinding, path []string) error {
		if states[b.SkillID] == 2 {
			return nil
		}
		if states[b.SkillID] == 1 {
			issue("cycle", b.SkillName, strings.Join(path, " → "), false, "dependency cycle: "+strings.Join(append(path, b.SkillName), " → "))
			return nil
		}
		states[b.SkillID] = 1
		defer func() { states[b.SkillID] = 2 }()
		m, err := s.ReadSkillManifest(ctx, b.VersionID)
		if err != nil {
			issue("manifest_invalid", b.SkillName, b.SkillName, false, err.Error())
			return nil
		}
		for _, r := range m.Environment {
			p.Requirements = append(p.Requirements, ManifestRequirement{SkillName: b.SkillName, SkillID: b.SkillID, RuntimeRequirement: r})
		}
		for _, d := range m.Dependencies {
			candidates := boundNames[d.SkillName]
			bound := len(candidates) > 0
			if !bound {
				if err := loadLibrary(); err != nil {
					return err
				}
				candidates = libraryNames[d.SkillName]
			}
			if len(candidates) == 0 {
				issue("missing_skill", d.SkillName, b.SkillName, d.Optional, "skill is not imported: "+d.SkillName)
				continue
			}
			if len(candidates) > 1 {
				issue("ambiguous_skill", d.SkillName, b.SkillName, d.Optional, "multiple imported skills match; explicitly select one: "+d.SkillName)
				continue
			}
			candidate := candidates[0]
			if d.ContentSHA256 != "" {
				hash, err := contentHash(candidate.VersionID)
				if err != nil {
					return err
				}
				if hash != d.ContentSHA256 {
					if bound {
						issue("version_conflict", d.SkillName, b.SkillName, false, "selected skill content does not match required content_sha256")
						continue
					}
					versions, err := s.Ver.ListVersions(ctx, version.EntitySkill, candidate.SkillID)
					if err != nil {
						return err
					}
					candidate.VersionID = ""
					for _, v := range versions {
						hash, err := contentHash(v.ID)
						if err != nil {
							return err
						}
						if hash == d.ContentSHA256 {
							candidate.VersionID = v.ID
							break
						}
					}
					if candidate.VersionID == "" {
						issue("missing_version", d.SkillName, b.SkillName, d.Optional, "required skill content is not imported")
						continue
					}
				}
			}
			if d.Optional && !bound {
				issue("optional_available", d.SkillName, b.SkillName, true, "optional skill is available; select it to include it")
				continue
			}
			if !bound {
				if len(p.Bindings) >= 16 {
					issue("too_many_skills", d.SkillName, b.SkillName, false, "dependency closure exceeds the limit of 16 skills")
					continue
				}
				p.Bindings = append(p.Bindings, candidate)
				addNames(boundNames, candidate)
				p.Additions = append(p.Additions, candidate)
			}
			if err := walk(candidate, append(path, b.SkillName)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, b := range resolved {
		if err := walk(b, nil); err != nil {
			return p, fmt.Errorf("resolve dependencies: %w", err)
		}
	}
	p.Ready = p.Resolvable && len(p.Additions) == 0
	return p, nil
}
