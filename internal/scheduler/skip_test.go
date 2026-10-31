package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestSkipsFormat(t *testing.T) {
	if ParseSkips("") != nil || ParseSkips("not json") != nil || len(ParseSkips(`["bad","2026-11-02T03:00:00+08:00"]`)) != 1 {
		t.Fatal("parse")
	}
	if got := ParseSkips(`["2026-11-02T03:00:00+08:00"]`)[0]; got != "2026-11-01T19:00:00Z" {
		t.Fatalf("not UTC: %s", got)
	}
	if FormatSkips(nil) != "" || FormatSkips([]string{"b", "a", "b"}) != `["a","b"]` {
		t.Fatal("format")
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if l := FutureSkips([]string{"2026-11-01T11:00:00Z", "2026-11-01T12:00:00Z", "2026-11-01T13:00:00Z"}, now); len(l) != 1 || l[0] != "2026-11-01T13:00:00Z" {
		t.Fatalf("future %v", l)
	}
	s := New(nil, nil, time.Hour, nil)
	s.SetLocation(time.UTC)
	nx, _ := s.NextSkipping("0 * * * *", now, []string{"2026-11-01T13:00:00Z", "2026-11-01T14:00:00Z"})
	if nx.UTC().Format(time.RFC3339) != "2026-11-01T15:00:00Z" {
		t.Fatalf("hop %s", nx)
	}
	if !s.IsFireTime("0 * * * *", now.Add(time.Hour), now, time.Hour*24) || s.IsFireTime("0 * * * *", now.Add(90*time.Minute), now, time.Hour*24) ||
		s.IsFireTime("0 * * * *", now, now, time.Hour) || s.IsFireTime("0 * * * *", now.Add(48*time.Hour), now, time.Hour*24) {
		t.Fatal("fire time")
	}
}

// A due task advances past skipped times (and forgets the ones behind it); owed fire times
// that were skipped are not caught up; Realign keeps a skip.
func TestSkipAdvanceAndRealign(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	hr := now.Truncate(time.Hour)
	f := func(n int) string { return hr.Add(time.Duration(n) * time.Hour).Format(time.RFC3339) }
	// due 3 hours ago; -2h was skipped, +1h and +2h are skipped
	skips := FormatSkips([]string{f(-2), f(1), f(2)})
	_ = store.InsertTask(ctx, db.Task{ID: "t1", Name: "a", Status: "active", Prompt: "p", CronExpr: "0 * * * *",
		OverlapPolicy: "skip", TimeoutMinutes: 5, NextRunAt: sql.NullString{String: f(-3), Valid: true}, CreatedAt: ts, UpdatedAt: ts})
	if _, err := sqlDB.Exec(`UPDATE tasks SET skip_runs=? WHERE id='t1'`, skips); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	var missed []time.Time
	s := New(store, nil, time.Hour, func(context.Context, db.Task) error { ran.Add(1); return nil })
	s.SetLocation(time.UTC)
	s.Catchup = func(_ db.Task, m []time.Time, _ time.Time) int { missed = m; return 1 }
	s.poll(ctx)
	if ran.Load() != 1 {
		t.Fatalf("ran %d", ran.Load())
	}
	for _, m := range missed {
		if m.UTC().Format(time.RFC3339) == f(-2) {
			t.Fatal("a skipped time counted as missed")
		}
	}
	if len(missed) != 3 { // -3, -1, 0
		t.Fatalf("missed %v", missed)
	}
	tk, _ := store.GetTask(ctx, "t1")
	if tk.NextRunAt.String != f(3) {
		t.Fatalf("next %s, want %s", tk.NextRunAt.String, f(3))
	}
	if got := ParseSkips(tk.SkipRuns); len(got) != 2 || got[0] != f(1) {
		t.Fatalf("skips left %v", got)
	}
	// Realign (start-up / timezone change) doesn't undo a skip
	if _, err := sqlDB.Exec(`UPDATE tasks SET next_run_at=? WHERE id='t1'`, f(2)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Realign(ctx, now); err != nil || n != 1 {
		t.Fatalf("realign %d %v", n, err)
	}
	if tk, _ := store.GetTask(ctx, "t1"); tk.NextRunAt.String != f(3) {
		t.Fatalf("realign next %s", tk.NextRunAt.String)
	}
	if n, _ := s.Realign(ctx, now); n != 0 {
		t.Fatalf("realign moved a skipped task again: %d", n)
	}
}
