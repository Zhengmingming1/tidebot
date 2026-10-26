package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// Wake runs on every poll, also while every schedule is paused (a task's own timed pause
// ends on time even then).
func TestWakeRunsWhilePaused(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(db.NewStore(sqlDB), nil, time.Hour, nil)
	n := 0
	s.Wake = func(context.Context, time.Time) { n++ }
	s.poll(context.Background())
	s.PauseUntil(true, time.Time{})
	s.poll(context.Background())
	if n != 2 {
		t.Fatalf("wake called %d times", n)
	}
}
