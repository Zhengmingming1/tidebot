package scheduler

import (
	"testing"
	"time"
)

func TestQuietNonexistentBoundaries(t *testing.T) {
	for _, tc := range []struct{ zone, date, gapTime, endTime string }{
		{"America/New_York", "2026-03-08", "02:30", "03:00"},
		{"Australia/Lord_Howe", "2026-10-04", "02:15", "02:30"},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			loc := mustLoc(t, tc.zone)
			at := func(clock string) time.Time {
				v, err := time.ParseInLocation("2006-01-02 15:04", tc.date+" "+clock, loc)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			boundary := at(tc.endTime)
			t.Run("end", func(t *testing.T) {
				q := Quiet{On: true, From: "23:00", To: tc.gapTime}
				before := boundary.Add(-time.Minute)
				in, end := q.At(before, loc)
				if !in || !end.Equal(boundary) {
					t.Fatalf("before jump: quiet=%v end=%v, want quiet through %v", in, end, boundary)
				}
				if in, _ := q.At(boundary, loc); in {
					t.Fatal("window must end at first valid time after gap")
				}
				if end := q.LastEnd(boundary, loc); !end.Equal(boundary) {
					t.Fatalf("LastEnd=%v, want %v", end, boundary)
				}
				s := New(nil, nil, time.Hour, nil)
				s.SetLocation(loc)
				s.SetQuiet(q)
				got := s.deferQuiet([]time.Time{at("00:30"), before}, boundary)
				if len(got) != 1 || !got[0].Equal(boundary) {
					t.Fatalf("deferred=%v, want one run at %v", got, boundary)
				}
			})
			t.Run("start", func(t *testing.T) {
				q := Quiet{On: true, From: tc.gapTime, To: "04:00"}
				if in, _ := q.At(boundary.Add(-time.Minute), loc); in {
					t.Fatal("quiet must not start before the skipped wall-clock time")
				}
				if in, end := q.At(boundary, loc); !in || !end.Equal(at("04:00")) {
					t.Fatalf("at gap end: quiet=%v end=%v", in, end)
				}
				if start := q.NextStart(at("00:00"), loc); !start.Equal(boundary) {
					t.Fatalf("NextStart=%v, want %v", start, boundary)
				}
			})
		})
	}
}

// Both edges in the same skipped interval collapse to an empty window, rather
// than moving a nonexistent quiet period into real time before or after the gap.
func TestQuietWindowEntirelyInGap(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Australia/Lord_Howe"} {
		t.Run(zone, func(t *testing.T) {
			loc := mustLoc(t, zone)
			day := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
			if zone == "Australia/Lord_Howe" {
				day = time.Date(2026, 10, 4, 12, 0, 0, 0, loc)
			}
			q := Quiet{On: true, From: "02:05", To: "02:25"}
			start, end, ok := q.window(day, loc)
			if ok || !start.Equal(end) {
				t.Fatalf("gap-only window: %v to %v, valid=%v", start, end, ok)
			}
			for _, at := range []time.Time{start.Add(-time.Minute), start, start.Add(time.Minute)} {
				if in, _ := q.At(at, loc); in {
					t.Fatalf("unexpected quiet at %v", at)
				}
			}
		})
	}
}

func TestQuietGapPreservesStartWeekday(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	before := time.Date(2026, 3, 8, 1, 59, 0, 0, loc) // Sunday, window began Saturday.
	boundary := time.Date(2026, 3, 8, 3, 0, 0, 0, loc)
	q := Quiet{On: true, From: "23:00", To: "02:30", Days: []int{int(time.Saturday)}}
	if in, end := q.At(before, loc); !in || !end.Equal(boundary) {
		t.Fatalf("Saturday overnight: quiet=%v end=%v", in, end)
	}
	q.Days = []int{int(time.Sunday)}
	if in, _ := q.At(before, loc); in {
		t.Fatal("Sunday start must not include Saturday's window")
	}
	q.From, q.To = "02:30", "04:00"
	if in, _ := q.At(boundary, loc); !in {
		t.Fatal("Sunday gap start must retain Sunday weekday")
	}
	q.Days = []int{int(time.Saturday)}
	if in, _ := q.At(boundary, loc); in {
		t.Fatal("Saturday restriction must exclude Sunday gap start")
	}
}

// Real boundaries, including the existing choice of a repeated wall time in
// autumn, are unchanged. This fix is deliberately limited to nonexistent times.
func TestQuietExistingBoundariesUnchanged(t *testing.T) {
	for _, tc := range []struct {
		zone           string
		month          time.Month
		day            int
		from, to       string
		fh, fm, th, tm int
	}{
		{"America/New_York", time.November, 1, "01:15", "01:45", 1, 15, 1, 45},
		{"Europe/Berlin", time.October, 25, "02:15", "02:45", 2, 15, 2, 45},
		{"America/New_York", time.March, 9, "02:15", "02:45", 2, 15, 2, 45},
	} {
		t.Run(tc.zone+tc.from+tc.month.String(), func(t *testing.T) {
			loc := mustLoc(t, tc.zone)
			day := time.Date(2026, tc.month, tc.day, 12, 0, 0, 0, loc)
			q := Quiet{On: true, From: tc.from, To: tc.to}
			start, end, ok := q.window(day, loc)
			wantStart := time.Date(2026, tc.month, tc.day, tc.fh, tc.fm, 0, 0, loc)
			wantEnd := time.Date(2026, tc.month, tc.day, tc.th, tc.tm, 0, 0, loc)
			if !ok || !start.Equal(wantStart) || !end.Equal(wantEnd) {
				t.Fatalf("window=%v..%v (%v), want %v..%v", start, end, ok, wantStart, wantEnd)
			}
		})
	}
}
