package skillstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// GetSetVersion reads a historical composition without following its current
// pointer. Deleted sets retain their history for existing task/run provenance.
func (s *Service) GetSetVersion(ctx context.Context, setID, versionID string) (SkillSet, error) {
	var out SkillSet
	if err := tasksvc.ValidateSkillSetSource(&tasksvc.SkillSetSource{SetID: setID, VersionID: versionID}); err != nil {
		return out, err
	}
	v, err := s.Ver.GetVersion(ctx, versionID)
	if err != nil {
		return out, err
	}
	if v.EntityType != SkillSetEntity || v.EntityID != setID {
		return out, fmt.Errorf("version does not belong to skill set")
	}
	if err = json.Unmarshal([]byte(v.ContentJSON), &out); err != nil {
		return out, err
	}
	out.ID, out.VersionID, out.VersionNo = setID, v.ID, v.VersionNo
	return out, nil
}

func (s *Service) ValidateSetSource(ctx context.Context, source *tasksvc.SkillSetSource) error {
	if source == nil {
		return nil
	}
	_, err := s.GetSetVersion(ctx, source.SetID, source.VersionID)
	return err
}

// SourceState compares declarations, ignoring display names. A follow binding
// is modified even if it currently resolves to the original pinned version.
func (s *Service) SourceState(ctx context.Context, source *tasksvc.SkillSetSource, bindings []tasksvc.SkillBinding) string {
	if source == nil {
		return "detached"
	}
	set, err := s.GetSetVersion(ctx, source.SetID, source.VersionID)
	if err != nil {
		return "missing"
	}
	if SameBindings(bindings, set.SkillBindings) {
		return "linked"
	}
	return "modified"
}

func SameBindings(a, b []tasksvc.SkillBinding) bool {
	aa, err := tasksvc.NormalizeBindings(a)
	if err != nil {
		return false
	}
	bb, err := tasksvc.NormalizeBindings(b)
	if err != nil {
		return false
	}
	for i := range aa {
		aa[i].SkillName = ""
	}
	for i := range bb {
		bb[i].SkillName = ""
	}
	return reflect.DeepEqual(aa, bb)
}

type BindingChange struct {
	Kind    string                `json:"kind"`
	SkillID string                `json:"skill_id"`
	Before  *tasksvc.SkillBinding `json:"before,omitempty"`
	After   *tasksvc.SkillBinding `json:"after,omitempty"`
}

func DiffBindings(before, after []tasksvc.SkillBinding) []BindingChange {
	changes := []BindingChange{}
	old := map[string]tasksvc.SkillBinding{}
	next := map[string]tasksvc.SkillBinding{}
	for _, b := range before {
		old[b.SkillID] = b
	}
	for _, b := range after {
		next[b.SkillID] = b
	}
	for _, b := range before {
		n, ok := next[b.SkillID]
		if !ok {
			changes = append(changes, BindingChange{Kind: "removed", SkillID: b.SkillID, Before: &b})
			continue
		}
		if b.Role != n.Role || b.Mode != n.Mode || b.VersionID != n.VersionID {
			changes = append(changes, BindingChange{Kind: "changed", SkillID: b.SkillID, Before: &b, After: &n})
		}
	}
	for _, b := range after {
		if _, ok := old[b.SkillID]; !ok {
			changes = append(changes, BindingChange{Kind: "added", SkillID: b.SkillID, After: &b})
		}
	}
	return changes
}

type RunSetSource struct {
	*tasksvc.SkillSetSource
	State string `json:"state"`
}

func (s *Service) RecordRunSource(ctx context.Context, runID string, source *tasksvc.SkillSetSource, declared []tasksvc.SkillBinding) error {
	return s.RecordRunSourceState(ctx, runID, source, s.SourceState(ctx, source, declared))
}

// RecordRunSourceState accepts the state derived from the declaration before a
// workflow freezes follow bindings. It is never accepted directly from the UI.
func (s *Service) RecordRunSourceState(ctx context.Context, runID string, source *tasksvc.SkillSetSource, state string) error {
	if source == nil {
		return nil
	}
	if err := s.ValidateSetSource(ctx, source); err != nil {
		return err
	}
	if state != "linked" && state != "modified" && state != "missing" {
		return fmt.Errorf("invalid composition source state")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO run_skill_set_sources(run_id,set_id,version_id,source_state) VALUES(?,?,?,?)`, runID, source.SetID, source.VersionID, state)
	return err
}

func (s *Service) RunSource(ctx context.Context, runID string) (*RunSetSource, error) {
	out := &RunSetSource{SkillSetSource: &tasksvc.SkillSetSource{}}
	err := s.DB.QueryRowContext(ctx, `SELECT set_id,version_id,source_state FROM run_skill_set_sources WHERE run_id=?`, runID).Scan(&out.SetID, &out.VersionID, &out.State)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
