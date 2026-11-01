package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/policy"
)

func TestQuietNormalize(t *testing.T) {
	q, err := Quiet{On: true, From: "7:05", To: "23:00", Days: []int{5, 1, 1, 3}}.Normalize()
	if err != nil || q.From != "07:05" || q.To != "23:00" || len(q.Days) != 3 || q.Days[0] != 1 || q.Days[2] != 5 {
		t.Fatalf("%+v %v", q, err)
	}
	if q, _ := (Quiet{From: "23:00", To: "07:00", Days: []int{0, 1, 2, 3, 4, 5, 6}}).Normalize(); q.Days != nil {
		t.Fatalf("all seven days = every day, got %v", q.Days)
	}
	for _, bad := range []Quiet{
		{From: "23:00", To: "23:00"},
		{From: "24:00", To: "07:00"},
		{From: "23:60", To: "07:00"},
		{From: "2300", To: "07:00"},
		{From: "23:00", To: "7"},
		{From: "", To: "07:00"},
		{From: "23:00", To: "07:00", Days: []int{7}},
		{From: "23:00", To: "07:00", Days: []int{-1}},
	} {
		if _, err := bad.Normalize(); err == nil {
			t.Errorf("want error for %+v", bad)
		}
	}
}

func TestQuietAtOvernightAndDays(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	q := Quiet{On: true, From: "23:00", To: "07:00"}
	cases := []struct {
		t   string
		in  bool
		end string
	}{
		{"2026-11-02 22:59", false, ""},
		{"2026-11-02 23:00", true, "2026-11-03 07:00"},
		{"2026-11-03 03:00", true, "2026-11-03 07:00"},
		{"2026-11-03 06:59", true, "2026-11-03 07:00"},
		{"2026-11-03 07:00", false, ""},
		{"2026-11-03 12:00", false, ""},
	}
	for _, c := range cases {
		in, end := q.At(at(c.t), loc)
		if in != c.in || (c.in && !end.Equal(at(c.end))) {
			t.Errorf("%s: in=%v end=%v", c.t, in, end)
		}
	}
	if in, _ := (Quiet{From: "23:00", To: "07:00"}).At(at("2026-11-03 03:00"), loc); in {
		t.Fatal("off never holds")
	}
	// Weekdays only (Mon–Fri starts): Friday night holds into Saturday morning,
	// Saturday night doesn't; Sunday night doesn't either (starts on Sunday).
	wd := Quiet{On: true, From: "23:00", To: "07:00", Days: []int{1, 2, 3, 4, 5}}
	// 2026-11-06 is a Friday
	if in, _ := wd.At(at("2026-11-07 02:00"), loc); !in {
		t.Fatal("Friday's window runs into Saturday")
	}
	if in, _ := wd.At(at("2026-11-08 02:00"), loc); in {
		t.Fatal("Saturday's window is off")
	}
	if in, _ := wd.At(at("2026-11-09 02:00"), loc); in {
		t.Fatal("Sunday's window is off")
	}
	if in, _ := wd.At(at("2026-11-10 02:00"), loc); !in {
		t.Fatal("Monday's window runs into Tuesday")
	}
	// Same-day window.
	day := Quiet{On: true, From: "12:00", To: "14:00"}
	if in, end := day.At(at("2026-11-03 13:00"), loc); !in || !end.Equal(at("2026-11-03 14:00")) {
		t.Fatal("midday window")
	}
	if in, _ := day.At(at("2026-11-03 01:00"), loc); in {
		t.Fatal("midday window doesn't hold at night")
	}
	if s := wd.NextStart(at("2026-11-07 12:00"), loc); !s.Equal(at("2026-11-09 23:00")) {
		t.Fatalf("next start after Saturday noon = Monday 23:00, got %v", s.In(loc))
	}
	if e := q.LastEnd(at("2026-11-03 10:00"), loc); !e.Equal(at("2026-11-03 07:00")) {
		t.Fatalf("last end %v", e.In(loc))
	}
}

// A window across a daylight-saving change ends at the wall-clock time, not after a
// fixed duration.
func TestQuietAtDST(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	q := Quiet{On: true, From: "23:00", To: "07:00"}
	// 2026-11-01 02:00 EDT → 01:00 EST
	start := time.Date(2026, 10, 31, 23, 30, 0, 0, loc)
	in, end := q.At(start, loc)
	if !in || end.In(loc).Hour() != 7 || end.In(loc).Day() != 1 {
		t.Fatalf("in=%v end=%v", in, end.In(loc))
	}
}

func TestQuietHoldsAndRunsOnceAtTheEnd(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	past := now.Add(-time.Minute).Format(time.RFC3339)
	if err := store.InsertTask(ctx, db.Task{ID: "tq", Name: "q", Status: "active", Prompt: "p", CronExpr: "*/1 * * * *",
		OverlapPolicy: "skip", TimeoutMinutes: 5, NextRunAt: sql.NullString{String: past, Valid: true}, CreatedAt: ts, UpdatedAt: ts}); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	s := New(store, nil, time.Hour, func(ctx context.Context, task db.Task) error { ran.Add(1); return nil })
	s.SetLocation(time.UTC)
	// A window around now.
	l := now.Add(-2 * time.Hour)
	f, e := l.Format("15:04"), now.Add(2*time.Hour).Format("15:04")
	s.SetQuiet(Quiet{On: true, From: f, To: e})
	if in, _ := s.QuietState(now); !in {
		t.Fatalf("window %s–%s should hold at %s", f, e, now.Format("15:04"))
	}
	s.poll(ctx)
	if ran.Load() != 0 {
		t.Fatal("quiet hours must not fire")
	}
	if tk, _ := store.GetTask(ctx, "tq"); tk.NextRunAt.String != past {
		t.Fatalf("held task stays due, next=%q", tk.NextRunAt.String)
	}
	// Off again: it fires.
	s.SetQuiet(Quiet{From: f, To: e})
	s.poll(ctx)
	if ran.Load() != 1 {
		t.Fatalf("ran=%d", ran.Load())
	}
}

// Missed slots inside a window that has ended fold into one, timed at the window's end,
// so even run_all comes back once and the catch-up window counts from the end.
func TestDeferQuiet(t *testing.T) {
	s := New(nil, nil, time.Hour, nil)
	loc := mustLoc(t, "Asia/Shanghai")
	s.SetLocation(loc)
	s.SetQuiet(Quiet{On: true, From: "23:00", To: "07:00"})
	at := func(h, m, d int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, loc) }
	var missed []time.Time
	for h := 23; h <= 30; h++ { // hourly 23:00 … 06:00
		missed = append(missed, at(h%24, 0, 2+h/24))
	}
	missed = append(missed, at(7, 0, 3), at(8, 0, 3))
	now := at(8, 30, 3)
	got := s.deferQuiet(missed, now)
	if len(got) != 3 || !got[0].Equal(at(7, 0, 3)) || !got[1].Equal(at(7, 0, 3)) || !got[2].Equal(at(8, 0, 3)) {
		t.Fatalf("got %v", got)
	}
	// 8 h window, 6 h grace (the default): still runs (counted from 07:00).
	only := s.deferQuiet(missed[:1], at(7, 0, 3).Add(15*time.Second))
	if n := policy.CatchupRuns("run_once", only, at(7, 0, 3).Add(15*time.Second), 6*time.Hour); n != 1 {
		t.Fatalf("run_once after quiet = %d", n)
	}
	if n := policy.CatchupRuns("run_all", s.deferQuiet(missed[:8], at(7, 0, 3).Add(15*time.Second)), at(7, 0, 3).Add(15*time.Second), 6*time.Hour); n != 1 {
		t.Fatalf("run_all after quiet = %d, want 1", n)
	}
	// "skip" runs it when tidebot was there at the end, not an hour later.
	if n := policy.CatchupRuns("skip", only, at(7, 0, 3).Add(15*time.Second), 6*time.Hour); n != 1 {
		t.Fatalf("skip on time = %d", n)
	}
	late := s.deferQuiet(missed[:1], at(8, 0, 3))
	if n := policy.CatchupRuns("skip", late, at(8, 0, 3), 6*time.Hour); n != 0 {
		t.Fatalf("skip late = %d", n)
	}
	// Window still going: nothing moves.
	if g := s.deferQuiet(missed[:2], at(1, 0, 3)); !g[0].Equal(missed[0]) || len(g) != 2 {
		t.Fatalf("open window must not move times: %v", g)
	}
	// Off: untouched.
	s.SetQuiet(Quiet{From: "23:00", To: "07:00"})
	if g := s.deferQuiet(missed, now); len(g) != len(missed) {
		t.Fatal("off must not touch missed times")
	}
}
