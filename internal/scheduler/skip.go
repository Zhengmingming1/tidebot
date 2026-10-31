package scheduler

import (
	"encoding/json"
	"sort"
	"time"
)

// Skipped fire times ("skip next run"): a task keeps its schedule but one or more of its
// upcoming fire times are left out. They are stored on the task (tasks.skip_runs) as a JSON
// array of RFC 3339 UTC strings; next_run_at always points past them, so everything that
// reads the next run (lists, Home, the due query) needs nothing extra.

// MaxSkips caps how many upcoming fire times one task can have skipped.
const MaxSkips = 20

// maxHops bounds the walk over skipped times (more than MaxSkips, so it always gets past them).
const maxHops = 64

// ParseSkips reads tasks.skip_runs; anything unreadable counts as none.
func ParseSkips(raw string) []string {
	if raw == "" {
		return nil
	}
	var in []string
	if json.Unmarshal([]byte(raw), &in) != nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			out = append(out, t.UTC().Format(time.RFC3339))
		}
	}
	return out
}

// FormatSkips is the stored form: sorted, without duplicates, ” when empty.
func FormatSkips(skips []string) string {
	if len(skips) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range skips {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	b, _ := json.Marshal(out)
	return string(b)
}

// FutureSkips keeps the skipped times after now (the ones that still mean something).
func FutureSkips(skips []string, now time.Time) []string {
	cut := now.UTC().Format(time.RFC3339)
	var out []string
	for _, s := range skips {
		if s > cut {
			out = append(out, s)
		}
	}
	return out
}

func skipSet(skips []string) map[string]bool {
	m := make(map[string]bool, len(skips))
	for _, s := range skips {
		m[s] = true
	}
	return m
}

// NextSkipping is NextRun, hopping over skipped fire times.
func (s *Scheduler) NextSkipping(expr string, from time.Time, skips []string) (time.Time, error) {
	nx, err := s.NextRun(expr, from)
	if err != nil || len(skips) == 0 {
		return nx, err
	}
	set := skipSet(skips)
	for i := 0; i < maxHops && !nx.IsZero() && set[nx.UTC().Format(time.RFC3339)]; i++ {
		if nx, err = s.NextRun(expr, nx); err != nil {
			return nx, err
		}
	}
	return nx, nil
}

// IsFireTime reports whether at is one of expr's fire times between now and now+within.
func (s *Scheduler) IsFireTime(expr string, at, now time.Time, within time.Duration) bool {
	if !at.After(now) || at.After(now.Add(within)) {
		return false
	}
	nx, err := s.NextRun(expr, at.Add(-time.Second))
	return err == nil && !nx.IsZero() && nx.Equal(at)
}
