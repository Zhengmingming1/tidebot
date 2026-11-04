package version

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestMovePointerRollsBackOnLogFailure(t *testing.T) {
	for _, operation := range []string{"create", "replace", "clear"} {
		t.Run(operation, func(t *testing.T) {
			sqlDB, err := db.Open(filepath.Join(t.TempDir(), "pointer.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			s := New(sqlDB)
			ctx := context.Background()
			first, err := s.Commit(ctx, CommitInput{EntityType: EntitySkill, EntityID: "skill_a", Content: "first", NoMove: true})
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.Commit(ctx, CommitInput{EntityType: EntitySkill, EntityID: "skill_a", Content: "second", NoMove: true})
			if err != nil {
				t.Fatal(err)
			}
			if operation != "create" {
				if err := s.MovePointer(ctx, EntitySkill, "skill_a", PointerCanary, first.Version.ID, "user", "seed", ""); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.ListOpLog(ctx, EntitySkill, "skill_a", 100)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sqlDB.Exec(`CREATE TRIGGER fail_pointer_log BEFORE INSERT ON operation_log WHEN NEW.action='pointer_move' BEGIN SELECT RAISE(ABORT, 'injected log failure'); END`)
			if err != nil {
				t.Fatal(err)
			}
			target := second.Version.ID
			if operation == "clear" {
				target = ""
			}
			err = s.MovePointer(ctx, EntitySkill, "skill_a", PointerCanary, target, "user", "test", "batch")
			if err == nil || !strings.Contains(err.Error(), "injected log failure") {
				t.Fatalf("error = %v", err)
			}
			pointer, err := s.GetPointer(ctx, EntitySkill, "skill_a", PointerCanary)
			if operation == "create" {
				if err != sql.ErrNoRows {
					t.Fatalf("failed create persisted pointer: %+v, %v", pointer, err)
				}
			} else if err != nil || !pointer.Valid || pointer.String != first.Version.ID {
				t.Fatalf("failed %s changed pointer: %+v, %v; want %s", operation, pointer, err, first.Version.ID)
			}
			after, err := s.ListOpLog(ctx, EntitySkill, "skill_a", 100)
			if err != nil || len(after) != len(before) {
				t.Fatalf("logs changed: %d -> %d, %v", len(before), len(after), err)
			}
			if _, err := sqlDB.Exec(`DROP TRIGGER fail_pointer_log`); err != nil {
				t.Fatal(err)
			}
			if err := s.MovePointer(ctx, EntitySkill, "skill_a", PointerCanary, target, "user", "retry", "batch"); err != nil {
				t.Fatal(err)
			}
			pointer, err = s.GetPointer(ctx, EntitySkill, "skill_a", PointerCanary)
			if err != nil || pointer != nullStr(target) {
				t.Fatalf("retry pointer=%+v, error=%v", pointer, err)
			}
			after, err = s.ListOpLog(ctx, EntitySkill, "skill_a", 100)
			if err != nil || len(after) != len(before)+1 {
				t.Fatalf("retry logs=%d, error=%v", len(after), err)
			}
			wantFrom := sql.NullString{}
			if operation != "create" {
				wantFrom = nullStr(first.Version.ID)
			}
			if after[0].FromVersionID != wantFrom || after[0].ToVersionID != nullStr(target) {
				t.Fatalf("retry audit=%+v", after[0])
			}
		})
	}
}

func TestMovePointerConcurrentAuditChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	const writers = 8
	services := make([]*Service, writers)
	for i := range services {
		sqlDB, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		services[i] = New(sqlDB)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	versions := make([]string, writers)
	for i := range versions {
		result, err := services[0].Commit(ctx, CommitInput{EntityType: EntitySkill, EntityID: "skill_a", Content: i, NoMove: true})
		if err != nil {
			t.Fatal(err)
		}
		versions[i] = result.Version.ID
	}
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i, s := range services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- s.MovePointer(ctx, EntitySkill, "skill_a", PointerCurrent, versions[i], "user", "concurrent", "batch")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	logs, err := services[0].ListOpLog(ctx, EntitySkill, "skill_a", writers)
	if err != nil || len(logs) != writers {
		t.Fatalf("logs=%d, %v", len(logs), err)
	}
	pointer, err := services[0].GetPointer(ctx, EntitySkill, "skill_a", PointerCurrent)
	if err != nil || pointer != logs[0].ToVersionID {
		t.Fatalf("pointer=%+v log=%+v err=%v", pointer, logs[0], err)
	}
	for i, entry := range logs {
		if entry.Action != "pointer_move" || entry.Actor != "user" || entry.Reason.String != "concurrent" || entry.BatchID.String != "batch" || entry.Pointer.String != PointerCurrent {
			t.Fatalf("unexpected metadata: %+v", entry)
		}
		if i+1 < len(logs) && entry.FromVersionID != logs[i+1].ToVersionID {
			t.Fatalf("broken audit chain: %+v -> %+v", logs[i+1], entry)
		}
	}
	if logs[len(logs)-1].FromVersionID.Valid {
		t.Fatal("first move must start with a NULL pointer")
	}
}

func TestMovePointerCanceledContext(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "canceled.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(sqlDB)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.MovePointer(ctx, EntityTask, "task_a", PointerCurrent, "", "user", "cancel", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if _, err := s.GetPointer(context.Background(), EntityTask, "task_a", PointerCurrent); err != sql.ErrNoRows {
		t.Fatalf("canceled call created a pointer: %v", err)
	}
	logs, err := s.ListOpLog(context.Background(), EntityTask, "task_a", 100)
	if err != nil || len(logs) != 0 {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
}
