package cost

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestDayBoundsFixedZone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// 2026-10-16 23:30 UTC is 2026-10-17 07:30 in Shanghai.
	st, en := DayBounds(time.Date(2026, 10, 16, 23, 30, 0, 0, time.UTC), loc)
	if got := st.UTC().Format(time.RFC3339); got != "2026-10-16T16:00:00Z" {
		t.Fatalf("start %s", got)
	}
	if en.Sub(st) != 24*time.Hour {
		t.Fatalf("len %v", en.Sub(st))
	}
}

func TestDayBoundsDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// spring forward 2026-03-08: 23h day
	st, en := DayBounds(time.Date(2026, 3, 8, 12, 0, 0, 0, loc), loc)
	if en.Sub(st) != 23*time.Hour {
		t.Fatalf("spring day len %v", en.Sub(st))
	}
	if got := st.UTC().Format(time.RFC3339); got != "2026-03-08T05:00:00Z" {
		t.Fatalf("start %s", got)
	}
	if got := en.UTC().Format(time.RFC3339); got != "2026-03-09T04:00:00Z" {
		t.Fatalf("end %s", got)
	}
	// fall back 2026-11-01: 25h day
	st, en = DayBounds(time.Date(2026, 11, 1, 12, 0, 0, 0, loc), loc)
	if en.Sub(st) != 25*time.Hour {
		t.Fatalf("fall day len %v", en.Sub(st))
	}
}

func TestBudgetResetsAtLocalMidnight(t *testing.T) {
	s, ctx := newTestSvc(t)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	s.Loc = loc
	// 23:30 local on Oct 16 (15:30 UTC)
	now := time.Date(2026, 10, 16, 23, 30, 0, 0, loc)
	s.Now = func() time.Time { return now }
	if err := s.Record(ctx, "r1", "", BucketProduction, 1.5, 10, 10); err != nil {
		t.Fatal(err)
	}
	sum, _ := s.Summary(ctx, "")
	if sum.TotalUSD != 1.5 || sum.Day != "2026-10-16" {
		t.Fatalf("before midnight: %+v", sum)
	}
	if sum.ResetsAt != "2026-10-16T16:00:00Z" {
		t.Fatalf("resets_at %s", sum.ResetsAt)
	}
	_ = s.UpsertBudget(ctx, Budget{Scope: "global", Period: "day", MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}})
	if c, _ := s.CheckBeforeRun(ctx, "", BucketProduction, 0.05); c.OK {
		t.Fatal("cap should block before local midnight")
	}
	// 00:10 local next day (still Oct 16 in UTC) → fresh budget
	now = time.Date(2026, 10, 17, 0, 10, 0, 0, loc)
	if c, _ := s.CheckBeforeRun(ctx, "", BucketProduction, 0.05); !c.OK {
		t.Fatalf("cap should reset at local midnight: %+v", c)
	}
	sum, _ = s.Summary(ctx, "")
	if sum.TotalUSD != 0 {
		t.Fatalf("after local midnight spend should reset, got %v", sum.TotalUSD)
	}
}

func TestBudgetDSTBoundary(t *testing.T) {
	s, ctx := newTestSvc(t)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	s.Loc = loc
	// 23:30 on spring-forward day (2026-03-08, EDT) = 03:30Z Mar 9
	now := time.Date(2026, 3, 8, 23, 30, 0, 0, loc)
	s.Now = func() time.Time { return now }
	_ = s.Record(ctx, "r1", "", BucketProduction, 2, 1, 1)
	if sum, _ := s.Summary(ctx, ""); sum.TotalUSD != 2 {
		t.Fatalf("same local day: %v", sum.TotalUSD)
	}
	now = time.Date(2026, 3, 9, 0, 5, 0, 0, loc)
	if sum, _ := s.Summary(ctx, ""); sum.TotalUSD != 0 {
		t.Fatalf("next local day should reset: %v", sum.TotalUSD)
	}
	if sum, _ := s.Summary(ctx, "2026-03-08"); sum.TotalUSD != 2 {
		t.Fatalf("explicit day: %v", sum.TotalUSD)
	}
}

func newTestSvc(t *testing.T) (*Service, context.Context) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return New(sqlDB, nil), context.Background()
}
