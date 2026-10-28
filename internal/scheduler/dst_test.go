package scheduler

import (
	"fmt"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func parseSpec(t *testing.T, expr string) cron.Schedule {
	t.Helper()
	s, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor).Parse(expr)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func walk(t *testing.T, expr string, from time.Time, loc *time.Location, n int) []string {
	t.Helper()
	s := parseSpec(t, expr)
	out := []string{}
	for i, f := 0, from; i < n; i++ {
		f = NextIn(s, f, loc)
		out = append(out, f.In(loc).Format("01-02 15:04 MST"))
	}
	return out
}

func TestNextInDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York") // 2026: forward 03-08 2:00→3:00, back 11-01 2:00→1:00
	lh := mustLoc(t, "Australia/Lord_Howe")
	spring := time.Date(2026, 3, 7, 12, 0, 0, 0, ny)
	fall := time.Date(2026, 10, 31, 12, 0, 0, 0, ny)
	cases := []struct {
		name, expr string
		from       time.Time
		loc        *time.Location
		want       string
	}{
		{"gap runs when the clock jumps", "30 2 * * *", spring, ny, "03-08 03:00 EDT|03-09 02:30 EDT|03-10 02:30 EDT"},
		{"gap weekly on the day", "0 2 * * 0", spring, ny, "03-08 03:00 EDT|03-15 02:00 EDT"},
		{"fold runs once", "0 1 * * *", fall, ny, "11-01 01:00 EDT|11-02 01:00 EST|11-03 01:00 EST"},
		{"fold runs once (half past)", "30 1 * * *", fall, ny, "11-01 01:30 EDT|11-02 01:30 EST"},
		{"created on the second pass", "0 1 * * *", time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), ny, "11-02 01:00 EST"},
		{"created on the first pass", "30 1 * * *", time.Date(2026, 11, 1, 5, 10, 0, 0, time.UTC), ny, "11-01 01:30 EDT|11-02 01:30 EST"},
		{"hourly keeps both passes", "0 * * * *", time.Date(2026, 10, 31, 23, 30, 0, 0, ny), ny, "11-01 00:00 EDT|11-01 01:00 EDT|11-01 01:00 EST|11-01 02:00 EST"},
		{"hourly skips the gap", "0 * * * *", time.Date(2026, 3, 8, 0, 30, 0, 0, ny), ny, "03-08 01:00 EST|03-08 03:00 EDT|03-08 04:00 EDT"},
		{"interval in the gap", "*/30 1-3 * * *", time.Date(2026, 3, 8, 0, 0, 0, 0, ny), ny, "03-08 01:00 EST|03-08 01:30 EST|03-08 03:00 EDT|03-08 03:30 EDT"},
		{"30-minute shift forward", "15 2 * * *", time.Date(2026, 10, 3, 12, 0, 0, 0, lh), lh, "10-04 02:30 +11|10-05 02:15 +11"},
		{"30-minute shift back", "45 1 * * *", time.Date(2026, 4, 4, 12, 0, 0, 0, lh), lh, "04-05 01:45 +11|04-06 01:45 +1030"},
	}
	for _, c := range cases {
		want := c.want
		n := len(splitBar(want))
		got := fmt.Sprint(joinBar(walk(t, c.expr, c.from, c.loc, n)))
		if got != want {
			t.Errorf("%s (%s):\n got %s\nwant %s", c.name, c.expr, got, want)
		}
	}
}

func splitBar(s string) []string {
	out, cur := []string{}, ""
	for _, r := range s {
		if r == '|' {
			out, cur = append(out, cur), ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func joinBar(a []string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += "|"
		}
		s += x
	}
	return s
}

// Over two years, a fixed-time daily job fires exactly once every calendar day, in zones
// with and without DST; interval schedules are what robfig computes.
func TestNextInOncePerDay(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Europe/London", "Australia/Lord_Howe", "America/Santiago", "Asia/Shanghai"} {
		loc := mustLoc(t, zone)
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
		from := start.Add(-time.Minute)
		for _, hm := range [][2]int{{0, 0}, {0, 30}, {1, 0}, {1, 30}, {2, 0}, {2, 30}, {3, 0}, {9, 0}, {23, 59}} {
			s := parseSpec(t, fmt.Sprintf("%d %d * * *", hm[1], hm[0]))
			days := map[string]int{}
			f := from
			for f.Before(start.AddDate(2, 0, 0)) {
				f = NextIn(s, f, loc)
				days[f.Format("2006-01-02")]++
			}
			for d := start; d.Before(start.AddDate(2, 0, 0)); d = d.AddDate(0, 0, 1) {
				k := d.Format("2006-01-02")
				if days[k] != 1 {
					t.Errorf("%s %02d:%02d: %s fired %d times", zone, hm[0], hm[1], k, days[k])
				}
			}
		}
		for _, expr := range []string{"*/15 * * * *", "0 * * * *", "0 9-17 * * 1-5", "30 */2 * * *", "@hourly"} {
			s := parseSpec(t, expr)
			if zone == "Australia/Lord_Howe" {
				continue // robfig skips days there (it assumes one-hour shifts)
			}
			a, b := start, start
			for i := 0; i < 3000; i++ {
				a, b = NextIn(s, a, loc), s.Next(b)
				if !a.Equal(b) {
					t.Fatalf("%s %s: %v != robfig %v", zone, expr, a, b)
				}
			}
		}
	}
}

func TestNextDSTNote(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	from := time.Date(2026, 1, 10, 12, 0, 0, 0, ny)
	year := 366 * 24 * time.Hour
	n := NextDSTNote(parseSpec(t, "30 2 * * *"), from, ny, year)
	if n == nil || n.Kind != "gap" || n.Date != "2026-03-08" || n.Wall != "02:30" || n.At.Format("15:04") != "03:00" {
		t.Fatalf("gap note: %+v", n)
	}
	n = NextDSTNote(parseSpec(t, "0 1 * * *"), from, ny, year)
	if n == nil || n.Kind != "fold" || n.Date != "2026-11-01" || n.Wall != "01:00" || n.At.Format("15:04 MST") != "01:00 EDT" {
		t.Fatalf("fold note: %+v", n)
	}
	// Weekly on a weekday that misses the change days: nothing to say.
	if n := NextDSTNote(parseSpec(t, "30 2 * * 3"), from, ny, year); n != nil {
		t.Fatalf("Wednesday job: %+v", n)
	}
	if n := NextDSTNote(parseSpec(t, "0 9 * * *"), from, ny, year); n != nil {
		t.Fatalf("9:00 job: %+v", n)
	}
	if n := NextDSTNote(parseSpec(t, "0 * * * *"), from, ny, year); n != nil {
		t.Fatalf("interval job: %+v", n)
	}
	if n := NextDSTNote(parseSpec(t, "30 2 * * *"), from, mustLoc(t, "Asia/Shanghai"), year); n != nil {
		t.Fatalf("no DST zone: %+v", n)
	}
	if n := NextDSTNote(parseSpec(t, "30 2 * * *"), from, ny, 30*24*time.Hour); n != nil {
		t.Fatalf("beyond horizon: %+v", n)
	}
}

// The scheduler itself (UTC from, as read from the database) follows the rules.
func TestSchedulerNextRunDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	s := New(nil, nil, time.Second, nil)
	s.SetLocation(ny)
	from := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC) // 01:00 EDT, the first pass just ran
	nx, err := s.NextRun("0 1 * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	if got := nx.In(ny).Format("01-02 15:04 MST"); got != "11-02 01:00 EST" {
		t.Fatalf("next=%s", got)
	}
	nx, _ = s.NextRun("30 2 * * *", time.Date(2026, 3, 7, 8, 0, 0, 0, time.UTC))
	if got := nx.In(ny).Format("01-02 15:04 MST"); got != "03-08 03:00 EDT" {
		t.Fatalf("next=%s", got)
	}
}
