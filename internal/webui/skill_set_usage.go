package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/workflow"
)

type skillSetUsage struct {
	Kind              string                     `json:"kind"`
	ID                string                     `json:"id"`
	NodeID            string                     `json:"node_id,omitempty"`
	Name              string                     `json:"name"`
	ExpectedVersionID string                     `json:"expected_version_id,omitempty"`
	ExpectedVersion   int                        `json:"expected_version,omitempty"`
	Source            *tasksvc.SkillSetSource    `json:"source"`
	State             string                     `json:"state"`
	CurrentBindings   []tasksvc.SkillBinding     `json:"current_bindings"`
	TargetBindings    []tasksvc.SkillBinding     `json:"target_bindings"`
	Changes           []skillstore.BindingChange `json:"changes"`
	CanApply          bool                       `json:"can_apply"`
}

func (s *Server) setUsage(ctx context.Context, source *tasksvc.SkillSetSource, bindings []tasksvc.SkillBinding, target skillstore.SkillSet) skillSetUsage {
	state := s.deps.Skills.SourceState(ctx, source, bindings)
	return skillSetUsage{Source: source, State: state, CurrentBindings: bindings, TargetBindings: target.SkillBindings,
		Changes: skillstore.DiffBindings(bindings, target.SkillBindings), CanApply: state == "linked" && source.VersionID != target.VersionID}
}

// Preview is read-only. Legacy bindings without provenance are never guessed.
func (s *Server) handleSkillSetUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	ctx := r.Context()
	setID := r.URL.Query().Get("set_id")
	current, err := s.deps.Skills.GetSet(ctx, setID)
	if err != nil {
		s.setError(w, err)
		return
	}
	target := current
	targetID := r.URL.Query().Get("target_version_id")
	if targetID == "" {
		targetID = r.URL.Query().Get("target_version")
	}
	if targetID != "" {
		target, err = s.deps.Skills.GetSetVersion(ctx, setID, targetID)
		if err != nil {
			s.setError(w, err)
			return
		}
	}
	versions, err := s.deps.Skills.Ver.ListVersions(ctx, skillstore.SkillSetEntity, setID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	history := []map[string]any{}
	for _, v := range versions {
		history = append(history, map[string]any{"version_id": v.ID, "version_no": v.VersionNo, "created_at": v.CreatedAt, "current": v.ID == current.VersionID})
	}
	usages := []skillSetUsage{}
	tasks, err := s.deps.Tasks.List(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, t := range tasks {
		if t.Spec == nil || t.Spec.SkillSetSource == nil || t.Spec.SkillSetSource.SetID != setID || t.Task.Status == "archived" {
			continue
		}
		u := s.setUsage(ctx, t.Spec.SkillSetSource, t.Spec.SkillBindings, target)
		u.Kind, u.ID, u.Name = "task", t.Task.ID, t.Task.Name
		if t.CurrentVersion != nil {
			u.ExpectedVersionID = t.CurrentVersion.ID
		}
		usages = append(usages, u)
	}
	if s.deps.Workflows != nil {
		flows, err := s.deps.Workflows.List(ctx)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for _, f := range flows {
			for _, n := range f.Definition.Nodes {
				if n.SkillSetSource == nil || n.SkillSetSource.SetID != setID {
					continue
				}
				u := s.setUsage(ctx, n.SkillSetSource, n.SkillBindings, target)
				u.Kind, u.ID, u.NodeID, u.Name, u.ExpectedVersion = "workflow_node", f.ID, n.ID, f.Name+" / "+n.Name, f.Version
				usages = append(usages, u)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"set_id": setID, "current_version_id": current.VersionID, "target_version_id": target.VersionID, "target": target, "versions": history, "usages": usages})
}

func (s *Server) usageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tasksvc.ErrConflict), errors.Is(err, workflow.ErrConflict):
		writeErr(w, 409, err.Error())
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, workflow.ErrNotFound):
		writeErr(w, 404, "target not found")
	default:
		writeErr(w, 400, err.Error())
	}
}

func (s *Server) upgradeBindings(ctx context.Context, source *tasksvc.SkillSetSource, bindings []tasksvc.SkillBinding, targetID string) (skillstore.SkillSet, error) {
	if source == nil || s.deps.Skills.SourceState(ctx, source, bindings) != "linked" {
		return skillstore.SkillSet{}, errors.New("composition was locally modified or detached; review and explicitly reapply it in the editor")
	}
	// Deleted compositions remain readable as provenance, but cannot be applied.
	if _, err := s.deps.Skills.GetSet(ctx, source.SetID); err != nil {
		return skillstore.SkillSet{}, err
	}
	return s.deps.Skills.GetSetVersion(ctx, source.SetID, targetID)
}

func (s *Server) handleSkillSetUsageApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var in struct {
		Target             string `json:"target"`
		ID                 string `json:"id"`
		NodeID             string `json:"node_id"`
		ExpectedVersionID  string `json:"expected_version_id"`
		ExpectedVersion    int    `json:"expected_version"`
		TargetSetVersionID string `json:"target_set_version_id"`
		Force              bool   `json:"force"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if in.Force || in.TargetSetVersionID == "" {
		writeErr(w, 400, "a target version is required; force replacement is not supported")
		return
	}
	ctx := r.Context()
	switch in.Target {
	case "task":
		if in.ExpectedVersionID == "" {
			writeErr(w, 400, "expected_version_id required")
			return
		}
		t, err := s.deps.Tasks.Get(ctx, in.ID)
		if err != nil {
			s.usageError(w, err)
			return
		}
		if t.Spec == nil || t.CurrentVersion == nil || t.Task.Status == "archived" {
			writeErr(w, 404, "task version not found")
			return
		}
		if t.CurrentVersion.ID != in.ExpectedVersionID {
			s.usageError(w, tasksvc.ErrConflict)
			return
		}
		target, err := s.upgradeBindings(ctx, t.Spec.SkillSetSource, t.Spec.SkillBindings, in.TargetSetVersionID)
		if err != nil {
			s.usageError(w, err)
			return
		}
		t.Spec.SkillBindings = target.SkillBindings
		t.Spec.SkillSetSource = &tasksvc.SkillSetSource{SetID: target.ID, VersionID: target.VersionID}
		saved, err := s.deps.Tasks.UpdateSpecExpected(ctx, in.ID, *t.Spec, "user", "explicit skill composition upgrade", in.ExpectedVersionID)
		if err != nil {
			s.usageError(w, err)
			return
		}
		writeJSON(w, 200, toTaskDTO(saved))
	case "workflow_node":
		if s.deps.Workflows == nil {
			writeErr(w, 503, "workflow service unavailable")
			return
		}
		if in.ExpectedVersion < 1 {
			writeErr(w, 400, "expected_version required")
			return
		}
		f, err := s.deps.Workflows.Get(ctx, in.ID)
		if err != nil {
			s.usageError(w, err)
			return
		}
		if f.Version != in.ExpectedVersion {
			s.usageError(w, workflow.ErrConflict)
			return
		}
		found := false
		for i := range f.Definition.Nodes {
			n := &f.Definition.Nodes[i]
			if n.ID != in.NodeID {
				continue
			}
			target, err := s.upgradeBindings(ctx, n.SkillSetSource, n.SkillBindings, in.TargetSetVersionID)
			if err != nil {
				s.usageError(w, err)
				return
			}
			n.SkillBindings = target.SkillBindings
			n.SkillSetSource = &tasksvc.SkillSetSource{SetID: target.ID, VersionID: target.VersionID}
			found = true
		}
		if !found {
			writeErr(w, 404, "workflow node not found")
			return
		}
		saved, err := s.deps.Workflows.Save(ctx, in.ID, in.ExpectedVersion, f.Definition)
		if err != nil {
			s.usageError(w, err)
			return
		}
		writeJSON(w, 200, saved)
	default:
		writeErr(w, 400, "target must be task or workflow_node")
	}
}
