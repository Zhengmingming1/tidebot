package tasksvc

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// SkillSetSource records the immutable composition a user explicitly applied.
// Nil means detached; membership may be edited without deleting its provenance.
type SkillSetSource struct {
	SetID     string `json:"set_id"`
	VersionID string `json:"version_id"`
}

var ErrConflict = errors.New("task changed; reload before saving")

// A single core owns writes. Cover all task-spec writers, including rollback,
// so a preview's expected version and the pointer update cannot interleave.
var specWrites sync.Mutex

func ValidateSkillSetSource(source *SkillSetSource) error {
	if source != nil && (!bindingID.MatchString(source.SetID) || !bindingID.MatchString(source.VersionID)) {
		return fmt.Errorf("invalid skill set source")
	}
	return nil
}

func (s *Service) validateSource(ctx context.Context, source *SkillSetSource) error {
	if err := ValidateSkillSetSource(source); err != nil {
		return err
	}
	if source == nil {
		return nil
	}
	v, err := s.Ver.GetVersion(ctx, source.VersionID)
	if err != nil {
		return fmt.Errorf("skill set source: %w", err)
	}
	if v.EntityType != "skill_set" || v.EntityID != source.SetID {
		return fmt.Errorf("skill set source version does not belong to set")
	}
	return nil
}
