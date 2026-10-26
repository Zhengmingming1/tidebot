package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // same zones on Windows

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// A from time in UTC (as parsed from the database) must not move the wall clock.
func TestNextRunReadsCronInSchedulerZone(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	s := New(nil, nil, time.Second, nil)
	s.SetLocation(sh)
	from := time.Date(2026, 10, 27, 1, 30, 0, 0, time.UTC) // 9:30 in Shanghai
	for _, f := range []time.Time{from, from.In(sh), from.In(time.FixedZone("x", -7*3600))} {
		nx, err := s.NextRun("0 9 * * *", f)
		if err != nil {
			t.Fatal(err)
		}
		if got := nx.In(sh).Format("2006-01-02 15:04"); got != "2026-10-28 09:00" {
			t.Fatalf("from %v: next=%s", f, got)
		}
		if nx.Location() != f.Location() {
			t.Fatalf("result should keep from's zone, got %v", nx.Location())
		}
	}
	if s.Location() != sh {
		t.Fatal("Location")
	}
	if New(nil, nil, time.Second, nil).Location() != time.Local {
		t.Fatal("default zone should be Local")
	}
}

// Over a DST change a daily task stays at the same wall-clock hour.
func TestNextRunAcrossDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	s := New(nil, nil, time.Second, nil)
	s.SetLocation(ny)
	from := time.Date(2026, 3, 7, 15, 0, 0, 0, time.UTC) // 10:00 EST, DST starts 3/8
	want := []string{"2026-03-08 09:00 EDT", "2026-03-09 09:00 EDT"}
	for _, w := range want {
		nx, err := s.NextRun("0 9 * * *", from)
		if err != nil {
			t.Fatal(err)
		}
		if got := nx.In(ny).Format("2006-01-02 15:04 MST"); got != w {
			t.Fatalf("next=%s want %s", got, w)
		}
		from = nx.UTC()
	}
}

func openStore(t *testing.T) *db.Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return db.NewStore(sqlDB)
}

func insertCronTask(t *testing.T, store *db.Store, id, expr, next string) {
	t.Helper()
	ts := time.Now().UTC().Format(time.RFC3339)
	tk := db.Task{ID: id, Name: id, Status: "active", Prompt: "p", CronExpr: expr, OverlapPolicy: "skip",
		TimeoutMinutes: 5, CreatedAt: ts, UpdatedAt: ts}
	if next != "" {
		tk.NextRunAt = sql.NullString{String: next, Valid: true}
	}
	if err := store.InsertTask(context.Background(), tk); err != nil {
		t.Fatal(err)
	}
}

func nextOf(t *testing.T, store *db.Store, id string) string {
	t.Helper()
	tk, err := store.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk.NextRunAt.String
}

// After a scheduled fire the stored next run is the same wall-clock time the next day,
// not the cron read in UTC (the bug: 9:00 Shanghai, then 17:00 Shanghai).
func TestFireKeepsWallClock(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	store := openStore(t)
	ctx := context.Background()
	insertCronTask(t, store, "daily", "0 9 * * *", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339))
	s := New(store, nil, time.Hour, func(context.Context, db.Task) error { return nil })
	s.SetLocation(sh)
	s.poll(ctx)
	nx, err := time.Parse(time.RFC3339, nextOf(t, store, "daily"))
	if err != nil {
		t.Fatal(err)
	}
	if l := nx.In(sh); l.Hour() != 9 || l.Minute() != 0 || !nx.After(time.Now()) || nx.Sub(time.Now()) > 24*time.Hour {
		t.Fatalf("next run %v (Shanghai %v) should be the coming 9:00 in Shanghai", nx, l)
	}
}

func TestRealign(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 27, 1, 30, 0, 0, time.UTC)                  // 9:30 Shanghai
	right := "2026-10-28T01:00:00Z"                                        // 9:00 Shanghai tomorrow
	insertCronTask(t, store, "wrong", "0 9 * * *", "2026-10-27T09:00:00Z") // 9:00 UTC = 17:00 Shanghai
	insertCronTask(t, store, "ok", "0 9 * * *", right)
	insertCronTask(t, store, "due", "0 9 * * *", "2026-10-26T09:00:00Z") // past: catch-up rules decide
	insertCronTask(t, store, "bad", "not cron", "2026-10-29T00:00:00Z")
	insertCronTask(t, store, "manual", "", "")
	s := New(store, nil, time.Hour, nil)
	s.SetLocation(sh)
	n, err := s.Realign(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for id, want := range map[string]string{"wrong": right, "ok": right, "due": "2026-10-26T09:00:00Z",
		"bad": "2026-10-29T00:00:00Z", "manual": ""} {
		if got := nextOf(t, store, id); got != want {
			t.Fatalf("%s: next=%q want %q", id, got, want)
		}
	}
	if n, _ := s.Realign(ctx, now); n != 0 {
		t.Fatalf("second realign changed %d", n)
	}
	// A timezone change moves future runs to the new wall clock.
	s.SetLocation(time.UTC)
	if n, _ := s.Realign(ctx, now); n != 2 {
		t.Fatalf("zone change: n=%d", n)
	}
	if got := nextOf(t, store, "ok"); got != "2026-10-27T09:00:00Z" {
		t.Fatalf("after zone change next=%q", got)
	}
}
