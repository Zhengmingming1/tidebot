package tasksvc

import "testing"

// A copy is paused with a reason and a time from the moment it exists (stored, not
// only in the returned view), and resuming it clears both like any other pause.
func TestDuplicateRecordsPauseReason(t *testing.T) {
	s, ctx := atomicFixture(t)
	src, err := s.Create(ctx, CreateInput{Name: "日报 «src»", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.Duplicate(ctx, src.Task.ID, "日报 2", "user")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Task.PauseKind != PauseCopy || cp.Task.PauseDetail != "日报 «src»" || !cp.Task.PausedAt.Valid {
		t.Fatalf("returned %+v", cp.Task)
	}
	got, err := s.Get(ctx, cp.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Status != "paused" || got.Task.PauseKind != PauseCopy || got.Task.PauseDetail != "日报 «src»" ||
		got.Task.PausedAt.String != cp.Task.PausedAt.String {
		t.Fatalf("stored %+v", got.Task)
	}
	// a later automatic pause does not relabel it (first reason wins)
	if err := s.Pause(ctx, cp.Task.ID, PauseBudget, "x"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, cp.Task.ID); got.Task.PauseKind != PauseCopy {
		t.Fatalf("relabelled %q", got.Task.PauseKind)
	}
	if err := s.SetStatus(ctx, cp.Task.ID, "active", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, cp.Task.ID); got.Task.Status != "active" || got.Task.PauseKind != "" || got.Task.PauseDetail != "" || got.Task.PausedAt.Valid {
		t.Fatalf("after resume %+v", got.Task)
	}
	// the source is untouched
	if sv, _ := s.Get(ctx, src.Task.ID); sv.Task.Status != "active" || sv.Task.PauseKind != "" {
		t.Fatalf("source %+v", sv.Task)
	}
}
