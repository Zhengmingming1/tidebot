package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// SkillReconciliation freezes the exact local version shown in an update card.
// It is separate from the immutable upstream snapshot and is never model authority.
type SkillReconciliation struct {
	SkillID            string `json:"skill_id"`
	Name               string `json:"name"`
	Type               string `json:"type"`
	Path               string `json:"path,omitempty"`
	InstalledVersionID string `json:"installed_version_id"`
	CurrentVersionID   string `json:"current_version_id"`
	FilesSHA256        string `json:"files_sha256"`
	LocallyModified    bool   `json:"locally_modified"`
	Removed            bool   `json:"removed,omitempty"`
}

type SkillResolution struct {
	Action      string `json:"action"`
	CopySkillID string `json:"copy_skill_id,omitempty"`
}

var ErrLocalSkillsChanged = errors.New("local skills changed; create a new update proposal and review again")

// ComponentIdentity uses the source path, not the mutable display name.
func ComponentIdentity(c Component) string {
	return c.Type + ":" + strings.TrimPrefix(strings.TrimSuffix(c.Path, "/"), "./")
}

type skillQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func skillState(ctx context.Context, q skillQuery, c Component) (SkillReconciliation, bool, error) {
	out := SkillReconciliation{SkillID: c.SkillID, Type: c.Type, Path: c.Path, InstalledVersionID: c.VersionID}
	var owned int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM operation_log WHERE entity_type='skill' AND entity_id=? AND actor='capability:ui'`, c.SkillID).Scan(&owned); err != nil {
		return out, false, err
	}
	// Never adopt an unrelated or legacy external skill just because its ID appears
	// in a lock. These registrations are outside capability ownership.
	if owned == 0 {
		return out, false, nil
	}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT s.name,v.id,v.content_json FROM skills s JOIN pointers p ON p.entity_type='skill' AND p.entity_id=s.id AND p.name='current' JOIN versions v ON v.id=p.version_id AND v.entity_type='skill' AND v.entity_id=s.id WHERE s.id=?`, c.SkillID).Scan(&out.Name, &out.CurrentVersionID, &raw)
	if err != nil {
		return out, false, fmt.Errorf("capability skill registration unavailable: %w", err)
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
		return out, false, err
	}
	if len(manifest.Files) == 0 {
		return out, false, errors.New("capability skill has no files")
	}
	data, err := json.Marshal(manifest.Files)
	if err != nil {
		return out, false, err
	}
	out.FilesSHA256 = digest(data)
	out.LocallyModified = c.LocallyModified || out.CurrentVersionID != c.VersionID
	return out, true, nil
}

func localSkillStates(ctx context.Context, q skillQuery, lock Lock) ([]SkillReconciliation, error) {
	out := []SkillReconciliation{}
	seen := map[string]bool{}
	for _, c := range lock.Components {
		if c.Disabled || c.SkillID == "" || c.Type != "skill" && c.Type != "command" {
			continue
		}
		if seen[c.SkillID] {
			return nil, errors.New("ambiguous capability skill identity")
		}
		seen[c.SkillID] = true
		state, owned, err := skillState(ctx, q, c)
		if err != nil {
			return nil, err
		}
		if owned {
			out = append(out, state)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkillID < out[j].SkillID })
	return out, nil
}

func (s *Store) populateLocalSkills(ctx context.Context, r *Record) error {
	var err error
	r.LocalSkills, err = localSkillStates(ctx, s.DB, r.Lock)
	for _, state := range r.LocalSkills {
		r.LocallyModified = r.LocallyModified || state.LocallyModified
	}
	return err
}

func reconcileStates(states []SkillReconciliation, candidate Lock) ([]SkillReconciliation, error) {
	identities := map[string]bool{}
	for _, c := range candidate.Components {
		if c.Disabled || c.Type != "skill" && c.Type != "command" {
			continue
		}
		key := ComponentIdentity(c)
		if identities[key] {
			return nil, errors.New("ambiguous capability component identity")
		}
		identities[key] = true
	}
	out := append([]SkillReconciliation{}, states...)
	for i := range out {
		out[i].Removed = !identities[ComponentIdentity(Component{Type: out[i].Type, Path: out[i].Path})]
	}
	return out, nil
}

func ValidateLocalChoices(p Proposal, choices map[string]string) error {
	known := map[string]bool{}
	for _, state := range p.SkillReconciliation {
		known[state.SkillID] = true
		action := choices[state.SkillID]
		if state.LocallyModified && action == "" {
			return errors.New("choose keep local, save a copy, or adopt upstream for each modified skill")
		}
		if action != "" && action != "keep_local" && action != "copy" && action != "adopt_upstream" {
			return errors.New("invalid local skill choice")
		}
	}
	for sid := range choices {
		if !known[sid] {
			return errors.New("local choice does not belong to this proposal")
		}
	}
	return nil
}

func (s *Store) CheckSkillReconciliation(ctx context.Context, p Proposal) error {
	if p.Kind != "update" {
		return nil
	}
	current, err := s.Get(ctx, p.Lock.ID)
	if err != nil {
		return err
	}
	if current.VersionID != p.BaseVersionID {
		return ErrLocalSkillsChanged
	}
	states, err := reconcileStates(current.LocalSkills, p.Lock)
	if err != nil {
		return err
	}
	if !slices.Equal(states, p.SkillReconciliation) {
		return ErrLocalSkillsChanged
	}
	return nil
}

// applySkillReconciliation runs inside snapshot publication. The pointer compare,
// moves and audit events commit together, so concurrent edits cannot be lost.
func applySkillReconciliation(ctx context.Context, tx *sql.Tx, p proposalPayload, lock Lock, resolutions map[string]SkillResolution, now string) error {
	choices := map[string]string{}
	for sid, r := range resolutions {
		choices[sid] = r.Action
	}
	if err := ValidateLocalChoices(Proposal{SkillReconciliation: p.SkillReconciliation}, choices); err != nil {
		return err
	}
	for _, reviewed := range p.SkillReconciliation {
		before := Component{Type: reviewed.Type, Path: reviewed.Path, SkillID: reviewed.SkillID, VersionID: reviewed.InstalledVersionID}
		state, owned, err := skillState(ctx, tx, before)
		if err != nil {
			return err
		}
		if !owned || state.CurrentVersionID != reviewed.CurrentVersionID || state.FilesSHA256 != reviewed.FilesSHA256 || state.Name != reviewed.Name {
			return ErrLocalSkillsChanged
		}
		resolution := resolutions[reviewed.SkillID]
		if resolution.Action == "" {
			resolution.Action = "adopt_upstream"
		}
		if resolution.Action == "copy" {
			if resolution.CopySkillID == "" || resolution.CopySkillID == reviewed.SkillID {
				return errors.New("local copy was not preserved")
			}
			copied, owned, err := skillState(ctx, tx, Component{SkillID: resolution.CopySkillID})
			if err != nil {
				return err
			}
			if !owned || copied.FilesSHA256 != reviewed.FilesSHA256 {
				return errors.New("local copy does not match reviewed files")
			}
		} else if resolution.CopySkillID != "" {
			return errors.New("unexpected local copy")
		}
		var next *Component
		for i := range lock.Components {
			c := &lock.Components[i]
			if !c.Disabled && ComponentIdentity(*c) == ComponentIdentity(before) {
				next = c
				break
			}
		}
		if reviewed.Removed {
			if next != nil {
				return errors.New("removed component changed since review")
			}
			// Removing a component never deletes its skill registration or history.
		} else {
			if next == nil || next.SkillID != reviewed.SkillID {
				return errors.New("update must preserve existing skill identity")
			}
			if resolution.Action == "keep_local" {
				if next.VersionID != reviewed.CurrentVersionID || next.FilesSHA256 != reviewed.FilesSHA256 || !next.LocallyModified {
					return errors.New("kept skill does not match reviewed local version")
				}
			} else {
				if next.LocallyModified {
					return errors.New("upstream skill cannot be marked as local")
				}
				var raw string
				if err = tx.QueryRowContext(ctx, `SELECT content_json FROM versions WHERE id=? AND entity_type='skill' AND entity_id=?`, next.VersionID, next.SkillID).Scan(&raw); err != nil {
					return err
				}
				var manifest struct {
					Files map[string]string `json:"files"`
				}
				if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
					return err
				}
				b, _ := json.Marshal(manifest.Files)
				if digest(b) != next.FilesSHA256 {
					return errors.New("updated skill hash mismatch")
				}
				result, err := tx.ExecContext(ctx, `UPDATE pointers SET version_id=?,updated_at=? WHERE entity_type='skill' AND entity_id=? AND name='current' AND version_id=?`, next.VersionID, now, next.SkillID, reviewed.CurrentVersionID)
				if err != nil {
					return err
				}
				n, _ := result.RowsAffected()
				if n != 1 {
					return ErrLocalSkillsChanged
				}
				if next.VersionID != reviewed.CurrentVersionID {
					if _, err = tx.ExecContext(ctx, `INSERT INTO operation_log(at,entity_type,entity_id,action,pointer,from_version_id,to_version_id,actor,reason) VALUES(?,'skill',?,'pointer_move','current',?,?,'capability:ui','reviewed capability update')`, now, next.SkillID, reviewed.CurrentVersionID, next.VersionID); err != nil {
						return err
					}
				}
			}
		}
		if reviewed.LocallyModified || resolution.Action != "adopt_upstream" {
			detail, _ := json.Marshal(struct {
				SkillID string `json:"skill_id"`
				SkillResolution
			}{reviewed.SkillID, resolution})
			if err = recordEvent(ctx, tx, lock.ID, "", "ui", "skill_reconcile", lock.Scan.Verdict, string(detail), now); err != nil {
				return err
			}
		}
	}
	return nil
}
