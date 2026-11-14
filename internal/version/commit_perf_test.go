package version

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

// TIDEBOT_PERF=1 go test -run TestCommitPerf -v ./internal/version
func TestCommitPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx := context.Background()
	const n = 2000
	for round := 0; round < 5; round++ {
		start := time.Now()
		for i := 0; i < n; i++ {
			if _, err := s.Commit(ctx, CommitInput{EntityType: EntityTask, EntityID: "t", Content: map[string]int{"r": round, "i": i}}); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("round %d: %v per commit", round, time.Since(start)/n)
	}
}
