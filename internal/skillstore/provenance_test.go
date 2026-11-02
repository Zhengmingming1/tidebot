package skillstore

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"testing"
)

func TestCompositionProvenanceHistoryAndModifiedDeclarations(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	set, err := s.SaveSet(ctx, "", "original", "", setMembers(t, s))
	if err != nil {
		t.Fatal(err)
	}
	source := &tasksvc.SkillSetSource{SetID: set.ID, VersionID: set.VersionID}
	if s.SourceState(ctx, source, set.SkillBindings) != "linked" {
		t.Fatal("pinned members should be linked")
	}
	renamed := append([]tasksvc.SkillBinding(nil), set.SkillBindings...)
	renamed[0].SkillName = "changed display name"
	if s.SourceState(ctx, source, renamed) != "linked" {
		t.Fatal("display name changed membership")
	}
	follow := append([]tasksvc.SkillBinding(nil), set.SkillBindings...)
	follow[0].Mode = "follow"
	follow[0].VersionID = ""
	if s.SourceState(ctx, source, follow) != "modified" {
		t.Fatal("follow must not silently retain immutable linkage")
	}
	if s.SourceState(ctx, nil, set.SkillBindings) != "detached" {
		t.Fatal("legacy is detached")
	}
	swapped := append([]tasksvc.SkillBinding(nil), set.SkillBindings...)
	swapped[0].Role, swapped[1].Role = "auxiliary", "primary"
	next, err := s.SaveSet(ctx, set.ID, "new name", set.VersionID, swapped)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.GetSetVersion(ctx, set.ID, set.VersionID)
	if err != nil || len(old.SkillBindings) != 2 || old.Name != "original" {
		t.Fatalf("old version %+v %v", old, err)
	}
	if _, err = s.GetSetVersion(ctx, "another_set", set.VersionID); err == nil {
		t.Fatal("cross-set source accepted")
	}
	changes := DiffBindings(old.SkillBindings, next.SkillBindings)
	if len(changes) != 2 || changes[0].Kind != "changed" {
		t.Fatalf("diff %+v", changes)
	}
	if err = s.DeleteSet(ctx, set.ID); err != nil {
		t.Fatal(err)
	}
	if s.SourceState(ctx, source, set.SkillBindings) != "linked" {
		t.Fatal("deleting catalog entry erased immutable source")
	}
}
