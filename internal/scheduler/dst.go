package scheduler

import (
	"math/bits"
	"time"

	"github.com/robfig/cron/v3"
)

// Daylight saving time rules (same as Hexagon/croner's fixed-time / interval split):
//
//   - A fixed-time job (one minute and one hour, e.g. "30 2 * * *") whose wall-clock time
//     doesn't exist that day (clocks jump forward) runs once when the jump ends (3:00),
//     if that is still the same day. robfig/cron skips the day.
//   - A fixed-time job whose wall-clock time occurs twice (clocks go back) runs only on the
//     first pass. robfig/cron runs it twice.
//   - Interval jobs ("*/15 * * * *", "0 * * * *", "0 9-17 * * *") keep robfig's behaviour:
//     times in a gap don't exist, times in the repeated hour run on both passes, so the
//     elapsed time between runs stays even.

const starBit = 1 << 63

// fixedTime reports the single hour and minute of a schedule, if it has exactly one of each.
func fixedTime(s cron.Schedule) (h, m int, ok bool) {
	spec, is := s.(*cron.SpecSchedule)
	if !is || spec.Location != time.Local { // CRON_TZ= prefix: leave as robfig computes it
		return 0, 0, false
	}
	hb, mb := spec.Hour&^starBit, spec.Minute&^starBit
	if bits.OnesCount64(hb) != 1 || bits.OnesCount64(mb) != 1 {
		return 0, 0, false
	}
	return bits.TrailingZeros64(hb), bits.TrailingZeros64(mb), true
}

// wall is t's wall clock as a UTC time (no DST), so robfig can walk wall-clock times.
func wall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// gapEnd returns the instant a spring-forward gap ends when wall clock w (a UTC "wall" time)
// doesn't exist in loc; ok is false when w exists.
func gapEnd(w time.Time, loc *time.Location) (time.Time, bool) {
	r := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), loc)
	if wall(r).Equal(w) {
		return time.Time{}, false
	}
	start, end := r.ZoneBounds()
	switch {
	case start.IsZero():
		return end, !end.IsZero()
	case end.IsZero():
		return start, true
	case r.Sub(start) <= end.Sub(r):
		return start, true
	default:
		return end, true
	}
}

// instants returns the instants whose wall clock in loc is w (a UTC "wall" time), earliest
// first: none in a spring-forward gap, two in a repeated (fall-back) stretch.
func instants(w time.Time, loc *time.Location) []time.Time {
	r := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), loc)
	out := []time.Time{}
	if wall(r).Equal(w) {
		out = append(out, r)
	}
	start, end := r.ZoneBounds()
	for _, edge := range []time.Time{start.Add(-time.Second), end} {
		if edge.IsZero() || edge.Equal(time.Time{}.Add(-time.Second)) {
			continue
		}
		_, off := edge.Zone()
		c := w.Add(-time.Duration(off) * time.Second).In(loc)
		if wall(c).Equal(w) && (len(out) == 0 || !c.Equal(out[0])) && (len(out) < 2 || !c.Equal(out[1])) {
			out = append(out, c)
		}
	}
	if len(out) == 2 && out[1].Before(out[0]) {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

// repeatPass reports whether t is on the second pass of a wall-clock time; first is the
// earlier instant with the same wall clock.
func repeatPass(t time.Time) (first time.Time, ok bool) {
	in := instants(wall(t), t.Location())
	if len(in) == 2 && in[1].Equal(t) {
		return in[0], true
	}
	return time.Time{}, false
}

// NextIn returns sched's next fire time after from with cron fields read in loc, applying
// the DST rules above. The result is in loc. Zero means never.
//
// The cron fields are matched against the wall clock (robfig walks a UTC copy, which has no
// DST), and each matching wall-clock time is then resolved to real instants. robfig's own
// DST handling assumes one-hour shifts and skips whole days in zones such as
// Australia/Lord_Howe (30 minutes).
func NextIn(sched cron.Schedule, from time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	from = from.In(loc)
	spec, ok := sched.(*cron.SpecSchedule)
	if !ok || spec.Location != time.Local { // @every, CRON_TZ=: leave as robfig computes it
		return sched.Next(from)
	}
	_, _, fixed := fixedTime(sched)
	found := walkWall(sched, wall(from), false, from, loc, fixed)
	if fixed {
		return found
	}
	// Interval jobs also run on the second pass of a repeated stretch. A walk from from's
	// wall clock can't see it when from is on the first pass, so walk again from the edge
	// where the clock goes back, and take the nearer match.
	_, end := from.ZoneBounds()
	if end.IsZero() || end.Sub(from) > 26*time.Hour {
		return found
	}
	_, before := from.Zone()
	_, after := end.Zone()
	if after >= before || (!found.IsZero() && found.Before(end)) {
		return found
	}
	other := walkWall(sched, wall(end.In(loc)), true, from, loc, fixed)
	if found.IsZero() || (!other.IsZero() && other.Before(found)) {
		return other
	}
	return found
}

// walkWall walks matching wall-clock times from start (inclusive or not) and returns the
// first instant after from that runs under the DST rules.
func walkWall(sched cron.Schedule, start time.Time, inclusive bool, from time.Time, loc *time.Location, fixed bool) time.Time {
	w := sched.Next(start)
	if inclusive {
		w = sched.Next(start.Add(-time.Nanosecond))
	}
	for i := 0; i < 10000 && !w.IsZero(); i, w = i+1, sched.Next(w) {
		in := instants(w, loc)
		switch {
		case len(in) == 0: // spring-forward gap
			if !fixed {
				continue
			}
			if end, ok := gapEnd(w, loc); ok {
				e := end.In(loc)
				if e.Year() == w.Year() && e.YearDay() == w.YearDay() && e.After(from) {
					return e
				}
			}
		case fixed:
			if in[0].After(from) {
				return in[0]
			}
		default:
			for _, c := range in {
				if c.After(from) {
					return c
				}
			}
		}
	}
	return time.Time{}
}

// DSTNote describes the next fire time the DST rules move or drop.
type DSTNote struct {
	// Kind: "gap" the time doesn't exist that day and runs at At instead; "skip" it doesn't
	// exist and the day has no later time to run at; "fold" it occurs twice and runs once, at At.
	Kind string    `json:"kind"`
	Date string    `json:"date"`        // local day, 2006-01-02
	Wall string    `json:"wall"`        // the planned wall-clock time, 15:04
	At   time.Time `json:"at,omitzero"` // when it actually runs
}

// NextDSTNote looks for the first DST adjustment of a fixed-time schedule within horizon
// (nil when there is none, or for interval schedules, which aren't adjusted).
func NextDSTNote(sched cron.Schedule, from time.Time, loc *time.Location, horizon time.Duration) *DSTNote {
	if loc == nil {
		loc = time.Local
	}
	h, m, ok := fixedTime(sched)
	if !ok {
		return nil
	}
	planned := time.Date(2000, 1, 1, h, m, 0, 0, time.UTC).Format("15:04")
	end := from.Add(horizon)
	t := from.In(loc)
	for i := 0; i < 8; i++ { // a zone changes offset a few times a year at most
		_, zend := t.ZoneBounds()
		if zend.IsZero() || zend.After(end) {
			return nil
		}
		// Matching wall-clock times within a day of the change.
		lo, hi := zend.Add(-26*time.Hour), zend.Add(26*time.Hour)
		if lo.Before(from) {
			lo = from
		}
		for w, j := sched.Next(wall(lo.In(loc))), 0; !w.IsZero() && w.Before(wall(hi.In(loc))) && j < 4; w, j = sched.Next(w), j+1 {
			in := instants(w, loc)
			switch len(in) {
			case 0:
				n := &DSTNote{Kind: "skip", Date: w.Format("2006-01-02"), Wall: planned}
				if e, ok := gapEnd(w, loc); ok {
					if e = e.In(loc); e.Year() == w.Year() && e.YearDay() == w.YearDay() {
						n.Kind, n.At = "gap", e
					}
				}
				return n
			case 2:
				if !in[0].Before(from) {
					return &DSTNote{Kind: "fold", Date: w.Format("2006-01-02"), Wall: planned, At: in[0]}
				}
			}
		}
		t = zend.Add(time.Second)
	}
	return nil
}
