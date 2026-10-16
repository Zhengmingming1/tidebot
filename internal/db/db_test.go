package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigrateAndCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := NewStore(sqlDB)
	ctx := context.Background()

	now := nowUTC()
	task := Task{
		ID: "task_1", Name: "demo", Status: "active", Prompt: "hello",
		WorkspaceDir: t.TempDir(), CronExpr: "*/5 * * * *", OverlapPolicy: "skip",
		TimeoutMinutes: 10, CreatedAt: now, UpdatedAt: now,
		NextRunAt: sql.NullString{String: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), Valid: true},
	}
	if err := store.InsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask(ctx, "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" {
		t.Fatalf("name=%s", got.Name)
	}
	due, err := store.ListDueTasks(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due=%d", len(due))
	}
	run := Run{
		ID: "run_1", TaskID: sql.NullString{String: "task_1", Valid: true},
		Status: "queued", TriggerKind: "cron", Prompt: "hello", CreatedAt: now,
	}
	if err := store.InsertRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	active, err := store.HasActiveRun(ctx, "task_1")
	if err != nil || !active {
		t.Fatalf("active=%v err=%v", active, err)
	}
	if err := store.MarkRunRunning(ctx, "run_1", time.Now()); err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	run.TotalCostUSD = sql.NullFloat64{Float64: 0.01, Valid: true}
	run.FinishedAt = sql.NullString{String: nowUTC(), Valid: true}
	if err := store.UpdateRunFinish(ctx, run); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListRuns(ctx, "task_1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "succeeded" {
		t.Fatalf("%+v", list)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db1.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
}
