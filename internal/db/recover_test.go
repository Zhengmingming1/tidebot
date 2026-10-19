package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoverInterruptedRuns(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := NewStore(sqlDB)
	ctx := context.Background()
	now := nowUTC()
	task := Task{ID: "task_1", Name: "backup", Status: "active", Prompt: "p", WorkspaceDir: t.TempDir(),
		CronExpr: "0 3 * * *", OverlapPolicy: "skip", TimeoutMinutes: 10, CreatedAt: now, UpdatedAt: now}
	if err := s.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	tid := sql.NullString{String: "task_1", Valid: true}
	for _, r := range []Run{
		{ID: "run_live", TaskID: tid, Status: "running", StartedAt: sql.NullString{String: now, Valid: true}, CreatedAt: now},
		{ID: "run_q", Status: "queued", CreatedAt: now},
		{ID: "run_ok", TaskID: tid, Status: "succeeded", CreatedAt: now, ErrorMessage: sql.NullString{}},
	} {
		if err := s.InsertRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// The orphan blocks the task's next scheduled fire (overlap=skip) until recovered.
	if active, _ := s.HasActiveRun(ctx, "task_1"); !active {
		t.Fatal("setup: expected an active run")
	}
	at := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	got, err := s.RecoverInterruptedRuns(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "run_live" || got[0].TaskID != "task_1" || got[1].ID != "run_q" || got[1].TaskID != "" {
		t.Fatalf("recovered %+v", got)
	}
	if active, _ := s.HasActiveRun(ctx, "task_1"); active {
		t.Fatal("task still blocked after recovery")
	}
	if n, _ := s.CountActiveRuns(ctx); n != 0 {
		t.Fatalf("active runs left: %d", n)
	}
	var st, msg, fin string
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message,''), COALESCE(finished_at,'') FROM runs WHERE id='run_live'`).Scan(&st, &msg, &fin); err != nil {
		t.Fatal(err)
	}
	if st != "canceled" || msg != RunInterruptedMsg || fin != "2026-10-19T02:00:00Z" {
		t.Fatalf("run_live = %s %q %s", st, msg, fin)
	}
	if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message,'') FROM runs WHERE id='run_ok'`).Scan(&st, &msg); err != nil || st != "succeeded" || msg != "" {
		t.Fatalf("finished run touched: %s %q %v", st, msg, err)
	}
	// Idempotent: a second start finds nothing.
	if again, err := s.RecoverInterruptedRuns(ctx, at); err != nil || len(again) != 0 {
		t.Fatalf("second pass %v %v", again, err)
	}
}
