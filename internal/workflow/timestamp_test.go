package workflow

import (
	"context"
	"sort"
	"testing"
	"time"
)

// Timestamps are compared as text (ORDER BY created_at), so text order must be
// time order even when a time falls on a whole second or ends in zeros.
func TestStampSortsLikeTime(t *testing.T) {
	base := time.Date(2026, 11, 24, 0, 0, 5, 0, time.UTC)
	times := []time.Time{
		base.Add(300 * time.Millisecond),
		base,
		base.Add(120 * time.Millisecond),
		base.Add(time.Second),
		base.Add(100 * time.Millisecond),
		base.Add(time.Nanosecond),
	}
	byText := append([]time.Time(nil), times...)
	sort.Slice(byText, func(i, j int) bool { return stamp(byText[i]) < stamp(byText[j]) })
	byTime := append([]time.Time(nil), times...)
	sort.Slice(byTime, func(i, j int) bool { return byTime[i].Before(byTime[j]) })
	for i := range byText {
		if !byText[i].Equal(byTime[i]) {
			t.Fatalf("text order differs at %d: %s vs %s", i, stamp(byText[i]), stamp(byTime[i]))
		}
	}
	if got := stamp(base); got != "2026-11-24T00:00:05.000000000Z" {
		t.Fatalf("whole second: %q", got)
	}
	if got := stamp(base.In(time.FixedZone("x", 11*3600))); got != "2026-11-24T00:00:05.000000000Z" {
		t.Fatalf("not UTC: %q", got)
	}
	if p, err := time.Parse(time.RFC3339Nano, stamp(base.Add(120*time.Millisecond))); err != nil || !p.Equal(base.Add(120*time.Millisecond)) {
		t.Fatalf("round trip: %v %v", p, err)
	}
}

// A real run writes fixed-width created/started/finished times on the run and its nodes.
func TestRunTimesFixedWidth(t *testing.T) {
	s := service(t, func(context.Context, Node, string, string, func(string)) (string, error) {
		return "ok", nil
	})
	w := save(t, s, skill("a"))
	rid, err := s.Start(context.Background(), w.ID, "in", "mock")
	if err != nil {
		t.Fatal(err)
	}
	r := finished(t, s, rid)
	check := func(name, v string) {
		t.Helper()
		if len(v) != len(tsLayout) {
			t.Fatalf("%s not fixed width: %q", name, v)
		}
		if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	check("created_at", r.CreatedAt)
	check("started_at", r.StartedAt)
	check("finished_at", r.FinishedAt)
	check("workflow created_at", w.CreatedAt)
	check("workflow updated_at", w.UpdatedAt)
	for _, n := range r.Nodes {
		check("node "+n.NodeID+" started_at", n.StartedAt)
		check("node "+n.NodeID+" finished_at", n.FinishedAt)
	}
}
