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
	all, err := s.List(ctx)
	if err != nil {
		return p, err
	}
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
	matches := func(b tasksvc.SkillBinding, name string) bool {
		for _, a := range aliases(b) {
			if a == name {
				return true
			}
		}
		return false
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
			candidates := []tasksvc.SkillBinding{}
			for _, candidate := range p.Bindings {
				if matches(candidate, d.SkillName) {
					candidates = append(candidates, candidate)
				}
			}
			bound := len(candidates) > 0
			if !bound {
				for _, sk := range all {
					if sk.CurrentVersion == nil {
						continue
					}
					candidate := tasksvc.SkillBinding{SkillID: sk.ID, SkillName: sk.Name, Mode: "pinned", VersionID: sk.CurrentVersion.ID, Role: "auxiliary"}
					if matches(candidate, d.SkillName) {
						candidates = append(candidates, candidate)
					}
				}
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
				hash, err := s.SkillContentSHA256(ctx, candidate.VersionID)
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
						hash, err := s.SkillContentSHA256(ctx, v.ID)
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
