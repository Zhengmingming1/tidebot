package policy

import (
	"context"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentOutcomesPreserveCountAndPolicy(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err = d.Exec(`INSERT INTO tasks(id,name,created_at,updated_at) VALUES('parallel','test',?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: d}
	ctx := context.Background()
	p := Default("parallel")
	p.RetryMax = 4
	p.Priority = "urgent"
	if err := s.Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.RecordOutcome(ctx, p.TaskID, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got := s.Get(ctx, p.TaskID)
	if got.ConsecutiveFailures != 20 || got.RetryMax != 4 || got.Priority != "urgent" {
		t.Fatalf("lost update: %+v", got)
	}
}
