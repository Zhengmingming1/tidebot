package tasksvc

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdateSpecExpectedSerializesConcurrentEditors(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "cas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := New(db.NewStore(d), version.New(d), nil)
	ctx := context.Background()
	original, err := s.Create(ctx, CreateInput{Name: "CAS", Prompt: "original"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			spec := *original.Spec
			spec.Prompt = fmt.Sprintf("editor %d", i)
			// Separate service instances still share the core's writer guard.
			other := New(db.NewStore(d), version.New(d), nil)
			_, e := other.UpdateSpecExpected(ctx, original.Task.ID, spec, "user", "edit", original.CurrentVersion.ID)
			results <- e
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	won, conflicts := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || conflicts != 15 {
		t.Fatalf("wins=%d conflicts=%d", won, conflicts)
	}
	versions, err := s.Ver.ListVersions(ctx, version.EntityTask, original.Task.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions=%d err=%v", len(versions), err)
	}
	got, _ := s.Get(ctx, original.Task.ID)
	if got.Task.Prompt != got.Spec.Prompt {
		t.Fatal("denormalized prompt diverged")
	}
	if _, err = s.UpdateSpecExpected(ctx, original.Task.ID, *got.Spec, "user", "stale no-op", original.CurrentVersion.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale no-op accepted: %v", err)
	}
	if err = s.Rollback(ctx, original.Task.ID, original.CurrentVersion.ID, "user", "rollback"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateSpecExpected(ctx, original.Task.ID, *got.Spec, "user", "after rollback", got.CurrentVersion.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("rollback missed by CAS: %v", err)
	}
}
