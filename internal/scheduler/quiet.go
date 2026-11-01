package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Quiet hours: a daily window (e.g. 23:00–07:00 in the scheduling timezone, optionally
// only on some weekdays) in which scheduled runs don't start. A task that comes due inside
// the window stays due; when the window ends it runs once, whatever its missed-run setting
// says about how many slots were missed (the window is a choice, not an outage), unless
// that setting is "skip" and tidebot wasn't running when the window ended. Runs started by
// hand, buttons and webhooks are not held.
//
// Idea from Uptime Kuma's recurring maintenance (server/model/maintenance.js:
// calcDuration adds 24 h when the end is before the start, generateCron's
// "recurring-weekday" fires on the listed weekdays at the start time, and
// getRunningTimeslot looks for the window that started within the last duration;
// src/pages/EditMaintenance.vue: a time range plus a Mon…Sun weekday picker).
// Here the end is a wall-clock time, so a window that crosses a daylight-saving change
// still ends at 07:00.

// Quiet is the stored quiet-hours setting.
type Quiet struct {
	On   bool   `json:"on"`
	From string `json:"from"` // "HH:MM", 24-hour
	To   string `json:"to"`   // "HH:MM"; earlier than From = ends the next day
	// Days the window starts on (0 = Sunday … 6 = Saturday); empty = every day.
	Days []int `json:"days,omitempty"`
}

// DefaultQuiet is what the form shows before anything was saved.
var DefaultQuiet = Quiet{From: "23:00", To: "07:00"}

// ParseHHMM reads "H:MM" / "HH:MM" (00:00–23:59) into minutes after midnight.
func ParseHHMM(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) < 1 || len(h) > 2 || len(m) != 2 {
		return 0, fmt.Errorf("time %q: want HH:MM", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("time %q: want HH:MM", s)
	}
	return hh*60 + mm, nil
}

// Normalize checks the setting and returns it with times as "HH:MM" and days sorted,
// de-duplicated (all seven = empty).
func (q Quiet) Normalize() (Quiet, error) {
	f, err := ParseHHMM(q.From)
	if err != nil {
		return q, err
	}
	t, err := ParseHHMM(q.To)
	if err != nil {
		return q, err
	}
	if f == t {
		return q, errors.New("quiet hours: start and end must differ")
	}
	out := Quiet{On: q.On, From: fmt.Sprintf("%02d:%02d", f/60, f%60), To: fmt.Sprintf("%02d:%02d", t/60, t%60)}
	seen := map[int]bool{}
	for _, d := range q.Days {
		if d < 0 || d > 6 {
			return q, fmt.Errorf("quiet hours: day %d: want 0 (Sunday) … 6", d)
		}
		if !seen[d] {
			seen[d] = true
			out.Days = append(out.Days, d)
		}
	}
	sort.Ints(out.Days)
	if len(out.Days) == 7 {
		out.Days = nil
	}
	return out, nil
}

func (q Quiet) startsOn(wd time.Weekday) bool {
	if len(q.Days) == 0 {
		return true
	}
	for _, d := range q.Days {
		if time.Weekday(d) == wd {
			return true
		}
	}
	return false
}

// window is the window that starts on the calendar day of day (in loc), if any.
func (q Quiet) window(day time.Time, loc *time.Location) (start, end time.Time, ok bool) {
	f, err1 := ParseHHMM(q.From)
	t, err2 := ParseHHMM(q.To)
	if err1 != nil || err2 != nil || f == t {
		return
	}
	y, m, d := day.In(loc).Date()
	if !q.startsOn(time.Date(y, m, d, 12, 0, 0, 0, loc).Weekday()) {
		return
	}
	start = time.Date(y, m, d, f/60, f%60, 0, 0, loc)
	ed := d
	if t < f {
		ed++
	}
	end = time.Date(y, m, ed, t/60, t%60, 0, 0, loc)
	return start, end, end.After(start)
}

// At reports whether t falls in a quiet window ([start, end)) and when that window ends.
// Off or invalid settings never hold.
func (q Quiet) At(t time.Time, loc *time.Location) (bool, time.Time) {
	if !q.On {
		return false, time.Time{}
	}
	if loc == nil {
		loc = time.Local
	}
	l := t.In(loc)
	// A window that holds t started today or yesterday (windows last under 24 h).
	for _, day := range []time.Time{l, time.Date(l.Year(), l.Month(), l.Day()-1, 12, 0, 0, 0, loc)} {
		if s, e, ok := q.window(day, loc); ok && !t.Before(s) && t.Before(e) {
			return true, e
		}
	}
	return false, time.Time{}
}

// NextStart is the start of the first quiet window after t (zero when off / none within
// a week + 1 day).
func (q Quiet) NextStart(t time.Time, loc *time.Location) time.Time {
	if !q.On {
		return time.Time{}
	}
	if loc == nil {
		loc = time.Local
	}
	l := t.In(loc)
	for i := 0; i <= 8; i++ {
		if s, _, ok := q.window(time.Date(l.Year(), l.Month(), l.Day()+i, 12, 0, 0, 0, loc), loc); ok && s.After(t) {
			return s
		}
	}
	return time.Time{}
}

// LastEnd is the end of the latest quiet window that is over at now (zero when off / none
// in the last two days).
func (q Quiet) LastEnd(now time.Time, loc *time.Location) time.Time {
	if !q.On {
		return time.Time{}
	}
	if loc == nil {
		loc = time.Local
	}
	l := now.In(loc)
	for i := 0; i <= 2; i++ {
		if _, e, ok := q.window(time.Date(l.Year(), l.Month(), l.Day()-i, 12, 0, 0, 0, loc), loc); ok && !e.After(now) {
			return e
		}
	}
	return time.Time{}
}

// SetQuiet sets the quiet hours (a normalized copy is kept; zero value = off).
func (s *Scheduler) SetQuiet(q Quiet) {
	c := q
	c.Days = append([]int(nil), q.Days...)
	s.quiet.Store(&c)
}

// Quiet is the current quiet-hours setting.
func (s *Scheduler) Quiet() Quiet {
	if q := s.quiet.Load(); q != nil {
		return *q
	}
	return Quiet{}
}

// QuietState reports whether scheduled runs are held for quiet hours at now, and until when.
func (s *Scheduler) QuietState(now time.Time) (bool, time.Time) {
	return s.Quiet().At(now, s.Location())
}

// deferQuiet moves missed fire times that fell in a quiet window which has ended by now to
// that window's end, and folds the ones of one window into a single slot: held runs come
// back once, and the catch-up window counts from when quiet hours ended.
func (s *Scheduler) deferQuiet(missed []time.Time, now time.Time) []time.Time {
	q := s.Quiet()
	if !q.On || len(missed) == 0 {
		return missed
	}
	loc := s.Location()
	out := make([]time.Time, 0, len(missed))
	var last time.Time
	for _, t := range missed {
		if in, end := q.At(t, loc); in && !end.After(now) {
			if end.Equal(last) {
				continue
			}
			t, last = end, end
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
