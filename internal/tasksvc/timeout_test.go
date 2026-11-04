package tasksvc

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"path/filepath"
	"testing"
)

func TestTimeoutUnlimitedDefaultAndRoundTrip(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := New(db.NewStore(d), version.New(d), nil)
	ctx := context.Background()
	v, err := s.Create(ctx, CreateInput{Name: "timeout", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Task.TimeoutMinutes != 0 || v.Spec.TimeoutMinutes != 0 {
		t.Fatal("new task is not unlimited")
	}
	unlimited := v.CurrentVersion.ID
	for _, minutes := range []int{17, 0, 1440} {
		sp := *v.Spec
		sp.TimeoutMinutes = minutes
		v, err = s.UpdateSpec(ctx, v.Task.ID, sp, "user", "timeout change")
		if err != nil {
			t.Fatal(err)
		}
		v, err = s.Get(ctx, v.Task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if v.Task.TimeoutMinutes != minutes || v.Spec.TimeoutMinutes != minutes {
			t.Fatalf("timeout not synchronized: %+v", v)
		}
	}
	if err = s.Rollback(ctx, v.Task.ID, unlimited, "user", "restore unlimited"); err != nil {
		t.Fatal(err)
	}
	v, err = s.Get(ctx, v.Task.ID)
	if err != nil || v.Task.TimeoutMinutes != 0 || v.Spec.TimeoutMinutes != 0 {
		t.Fatalf("rollback: %+v %v", v, err)
	}
	for _, n := range []int{-1, 1441} {
		if _, err = s.Create(ctx, CreateInput{Name: "bad", Prompt: "x", TimeoutMinutes: n}); err == nil {
			t.Fatalf("accepted %d", n)
		}
	}
}

func TestLegacyTimeoutOmissionPreservesTaskLimit(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := New(db.NewStore(d), version.New(d), nil)
	ctx := context.Background()
	v, err := s.Create(ctx, CreateInput{Name: "legacy", Prompt: "work", TimeoutMinutes: 17})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Ver.Commit(ctx, version.CommitInput{EntityType: version.EntityTask, EntityID: v.Task.ID, Content: map[string]any{"prompt": "old version"}, Source: "user_edit", Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.Get(ctx, v.Task.ID)
	if err != nil || v.Spec.TimeoutMinutes != 17 {
		t.Fatalf("legacy Get lost timeout: %+v %v", v, err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Spec.TimeoutMinutes != 17 {
		t.Fatalf("legacy List lost timeout: %+v %v", list, err)
	}
	sp := *v.Spec
	sp.TimeoutMinutes = 25
	v, err = s.UpdateSpec(ctx, v.Task.ID, sp, "user", "set timeout")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Rollback(ctx, v.Task.ID, legacy.Version.ID, "user", "legacy rollback"); err != nil {
		t.Fatal(err)
	}
	v, err = s.Get(ctx, v.Task.ID)
	if err != nil || v.Task.TimeoutMinutes != 25 || v.Spec.TimeoutMinutes != 25 {
		t.Fatalf("legacy rollback reset timeout: %+v %v", v, err)
	}
	sp = *v.Spec
	sp.TimeoutMinutes = 0
	v, err = s.UpdateSpec(ctx, v.Task.ID, sp, "user", "disable timeout")
	if err != nil {
		t.Fatal(err)
	}
	// An explicit zero in the new version wins even over an old denormalized row.
	if _, err = d.Exec(`UPDATE tasks SET timeout_minutes=17 WHERE id=?`, v.Task.ID); err != nil {
		t.Fatal(err)
	}
	v, err = s.Get(ctx, v.Task.ID)
	if err != nil || v.Spec.TimeoutMinutes != 0 {
		t.Fatalf("explicit zero inherited old timeout: %+v %v", v, err)
	}
}
