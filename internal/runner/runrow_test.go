package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

// TestRunRowWrittenWhole: the permission level, skill version and cost bucket
// go in with the run row. They used to be patched in by two follow-up UPDATEs
// whose errors were dropped; one that failed left the run with no run_mode
// (no 「改动的文件」 / undo, a continued session at the wrong level, 存为任务
// without the skill version). A trigger makes any later write of those
// columns fail, as a busy or closing database would.
func TestRunRowWrittenWhole(t *testing.T) {
	skipOnWindows(t)
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TRIGGER runs_meta_busy BEFORE UPDATE OF run_mode, skill_id, skill_version_id, cost_bucket ON runs
BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(sqlDB)
	r := New(store, nil, 2)
	type meta struct{ status, bucket, skill, ver, mode string }
	read := func(id string) (m meta) {
		if err := sqlDB.QueryRow(`SELECT status, cost_bucket, skill_id, skill_version_id, run_mode FROM runs WHERE id=?`, id).
			Scan(&m.status, &m.bucket, &m.skill, &m.ver, &m.mode); err != nil {
			t.Fatal(err)
		}
		return m
	}
	var atStart meta
	res, err := r.RunOnce(context.Background(), Options{
		ClaudeBin: mockBin(t), Prompt: "hello", WorkspaceDir: t.TempDir(), Timeout: 10 * time.Second, Bare: true,
		Profile: provider.Profile{Name: "mock", BaseURL: "http://127.0.0.1:9", AuthEnv: "ANTHROPIC_AUTH_TOKEN", Model: "mock-model"},
		APIKey:  "test-key-not-real", TriggerKind: "quick",
		CostBucket: "assist", SkillID: "skl_a", SkillVersionID: "ver_a", RunMode: "exec",
		OnStart: func(id string) { atStart = read(id) },
	})
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := meta{bucket: "assist", skill: "skl_a", ver: "ver_a", mode: "exec"}
	for name, got := range map[string]meta{"at start": atStart, "finished": read(res.RunID)} {
		got.status = ""
		if got != want {
			t.Fatalf("%s: %+v, want %+v", name, got, want)
		}
	}

	// Nothing set: the defaults the columns always had.
	res, err = r.RunOnce(context.Background(), Options{
		ClaudeBin: mockBin(t), Prompt: "plain", WorkspaceDir: t.TempDir(), Timeout: 10 * time.Second, Bare: true,
		Profile: provider.Profile{Name: "mock", BaseURL: "http://127.0.0.1:9", AuthEnv: "ANTHROPIC_AUTH_TOKEN", Model: "mock-model"},
		APIKey:  "test-key-not-real",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(res.RunID); got.bucket != "production" || got.skill != "" || got.ver != "" || got.mode != "" {
		t.Fatalf("defaults: %+v", got)
	}
}

// TestRunRowInsertPerf (TIDEBOT_PERF=1): one INSERT against the old INSERT +
// two UPDATEs per run, on a file database like the app's.
func TestRunRowInsertPerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("TIDEBOT_PERF=1")
	}
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := db.NewStore(sqlDB)
	ctx := context.Background()
	const n = 2000
	row := func(i int, tag string) db.Run {
		return db.Run{ID: fmt.Sprintf("run_%s_%d", tag, i), Status: "queued", TriggerKind: "quick", Prompt: "p",
			CreatedAt: time.Now().UTC().Format(time.RFC3339), ProviderName: sql.NullString{String: "mock", Valid: true}}
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		rw := row(i, "old")
		if err := store.InsertRun(ctx, rw); err != nil {
			t.Fatal(err)
		}
		_, _ = sqlDB.ExecContext(ctx, `UPDATE runs SET cost_bucket=? WHERE id=?`, "assist", rw.ID)
		_, _ = sqlDB.ExecContext(ctx, `UPDATE runs SET skill_id=?, skill_version_id=?, run_mode=? WHERE id=?`, "s", "v", "exec", rw.ID)
	}
	old := time.Since(start)
	start = time.Now()
	for i := 0; i < n; i++ {
		rw := row(i, "new")
		rw.CostBucket, rw.SkillID, rw.SkillVersionID, rw.RunMode = "assist", "s", "v", "exec"
		if err := store.InsertRun(ctx, rw); err != nil {
			t.Fatal(err)
		}
	}
	cur := time.Since(start)
	t.Logf("per run: insert+2 updates %v, one insert %v", old/n, cur/n)
}
