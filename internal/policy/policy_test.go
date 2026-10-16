package policy

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestClassify(t *testing.T) {
	cases := map[string]bool{"rate_limit_error": true, "API overloaded": true, "authentication_failed": false,
		"budget blocked: x": false, "exit status 1": true, "blocked by guard: rm -rf": false}
	for m, want := range cases {
		if _, r := Classify(m); r != want {
			t.Errorf("%q retry=%v", m, r)
		}
	}
}

func TestBackoff(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	if d := Backoff(1, r); d < 48*time.Second || d > 72*time.Second {
		t.Fatal(d)
	}
	if d := Backoff(2, r); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatal(d)
	}
}

func TestCatchup(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	missed := []time.Time{now.Add(-10 * time.Hour), now.Add(-3 * time.Hour), now.Add(-2 * time.Hour), now.Add(-1 * time.Hour)}
	g := 6 * time.Hour
	if n := CatchupRuns("run_once", missed, now, g); n != 1 {
		t.Fatal(n)
	}
	if n := CatchupRuns("run_all", missed, now, g); n != 3 {
		t.Fatal(n)
	}
	if n := CatchupRuns("skip", missed, now, g); n != 0 {
		t.Fatal(n)
	}
	if n := CatchupRuns("run_once", missed[:1], now, g); n != 0 {
		t.Fatal("outside grace", n)
	}
	if n := CatchupRuns("skip", []time.Time{now.Add(-20 * time.Second)}, now, g); n != 1 {
		t.Fatal("on-time slot must run under skip")
	}
}

func TestBreaker(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	_, _ = sqlDB.Exec(`INSERT INTO tasks(id,name,created_at,updated_at) VALUES('t1','x',?,?)`, ts, ts)
	s := &Store{DB: sqlDB}
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		n, tripped, err := s.RecordOutcome(ctx, "t1", false)
		if err != nil || n != i || tripped != (i == 3) {
			t.Fatalf("i=%d n=%d tripped=%v err=%v", i, n, tripped, err)
		}
	}
	if n, _, _ := s.RecordOutcome(ctx, "t1", true); n != 0 {
		t.Fatal("success resets")
	}
	p := s.Get(ctx, "t1")
	p.Catchup = "bogus"
	if err := s.Put(ctx, p); err == nil {
		t.Fatal("validate")
	}
}
