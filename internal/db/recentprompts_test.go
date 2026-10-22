package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRecentQuickPrompts(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := NewStore(sqlDB)
	ctx := context.Background()
	now := nowUTC()
	if err := s.InsertTask(ctx, Task{ID: "task_1", Name: "n", Status: "active", Prompt: "p", WorkspaceDir: t.TempDir(),
		OverlapPolicy: "skip", TimeoutMinutes: 10, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	tid := sql.NullString{String: "task_1", Valid: true}
	for i, r := range []Run{
		{Prompt: "old"},
		{Prompt: "dup"},
		{Prompt: "task prompt", TaskID: tid},
		{Prompt: "  "},
		{Prompt: "it's \"quoted\" '; DROP TABLE runs; --"},
		{Prompt: "dup"},
	} {
		r.ID, r.Status, r.CreatedAt = fmt.Sprintf("run_%d", i), "succeeded", fmt.Sprintf("2026-10-22T02:00:%02dZ", i)
		if err := s.InsertRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentQuickPrompts(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dup", "it's \"quoted\" '; DROP TABLE runs; --", "old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, _ := s.RecentQuickPrompts(ctx, 1); len(got) != 1 || got[0] != "dup" {
		t.Fatalf("limit: %q", got)
	}
}
