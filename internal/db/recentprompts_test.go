package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
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

// Recent prompts walk runs without a task newest first on the partial index and stop at
// the limit; migration 027 swaps the runs list indexes for ones that cover its sort key.
func TestRecentQuickPromptsIndex(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	idx := map[string]bool{}
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='runs'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		idx[n] = true
	}
	rows.Close()
	for _, n := range []string{"idx_runs_created_id", "idx_runs_status_created", "idx_runs_quick_created", "idx_runs_notask_created", "idx_runs_task_created", "idx_runs_status_finished"} {
		if !idx[n] {
			t.Errorf("missing index %s", n)
		}
	}
	for _, n := range []string{"idx_runs_created", "idx_runs_status"} {
		if idx[n] {
			t.Errorf("index %s should be gone (prefix of a newer one)", n)
		}
	}
	var plan []string
	pr, err := sqlDB.Query(`EXPLAIN QUERY PLAN SELECT prompt FROM runs INDEXED BY idx_runs_notask_created
WHERE (task_id IS NULL OR task_id = '') AND TRIM(prompt) <> ''
ORDER BY created_at DESC, id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	for pr.Next() {
		var a, b, c int
		var d string
		_ = pr.Scan(&a, &b, &c, &d)
		plan = append(plan, d)
	}
	pr.Close()
	if p := fmt.Sprint(plan); !strings.Contains(p, "idx_runs_notask_created") || strings.Contains(p, "TEMP B-TREE") {
		t.Fatalf("recent prompts plan: %s", p)
	}
	// equal times: the later id counts as newer; many repeats of one prompt still give the rest
	s := NewStore(sqlDB)
	ctx := context.Background()
	for i, p := range []string{"a", "b", "b", "b", "b", "b", "b", "b", "c"} {
		if err := s.InsertRun(ctx, Run{ID: fmt.Sprintf("run_%02d", i), Status: "succeeded", Prompt: p, CreatedAt: "2026-10-22T02:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.RecentQuickPrompts(ctx, 6); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Fatalf("got %q", got)
	}
}
