package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeOld(t *testing.T, path string, n int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	_ = os.Chtimes(path, ts, ts)
}

func callStorage(t *testing.T, a *App, method string, in any) (json.RawMessage, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	out, err := a.CallExt(context.Background(), method, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	return b, nil
}

func TestStorageUsagePreviewAndCleanup(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	logs := filepath.Join(a.Cfg.DataDir, "runs")
	day := 24 * time.Hour
	writeOld(t, filepath.Join(logs, "r_old.log"), 100, 40*day)
	writeOld(t, filepath.Join(logs, "r_live.log"), 50, 40*day) // still running: never deleted
	writeOld(t, filepath.Join(logs, "r_new.log"), 10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "artifacts", "r_old", "report.md"), 300, 40*day)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('r_live','running',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	// keep_per_task=0 so only age matters; artifacts kept forever.
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"keep_per_task": 0, "artifact_days": 0}); err != nil {
		t.Fatal(err)
	}
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Parts["logs"].Bytes != 160 || v.Parts["logs"].Count != 3 || v.Parts["artifacts"].Bytes != 300 || v.Parts["database"].Bytes == 0 {
		t.Fatalf("usage %+v", v.Parts)
	}
	if v.Preview.Files != 1 || v.Preview.Bytes != 100 || v.Policy.LogDays != 30 || v.Policy.ArtifactDays != 0 {
		t.Fatalf("preview %+v policy %+v", v.Preview, v.Policy)
	}
	if v.Last != nil {
		t.Fatal("no cleanup yet")
	}
	// The preview must not have touched anything.
	if _, err := os.Stat(filepath.Join(logs, "r_old.log")); err != nil {
		t.Fatal("preview deleted a file")
	}
	b, err := callStorage(t, a, "storage.cleanup", nil)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Report  RetentionReport `json:"report"`
		Storage StorageView     `json:"storage"`
	}
	_ = json.Unmarshal(b, &res)
	if res.Report.DeletedFiles != 1 || res.Report.FreedBytes != 100 || res.Storage.Preview.Files != 0 || res.Storage.Last == nil || res.Storage.Last.Auto {
		t.Fatalf("cleanup %s", b)
	}
	for _, keep := range []string{"r_live.log", "r_new.log"} {
		if _, err := os.Stat(filepath.Join(logs, keep)); err != nil {
			t.Fatalf("%s deleted", keep)
		}
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "artifacts", "r_old")); err != nil {
		t.Fatal("artifact deleted although kept forever")
	}
}

func TestStorageSettingsValidateAndPersist(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if got := a.RetentionSettings(ctx); got != DefaultRetention {
		t.Fatalf("default %+v", got)
	}
	for _, bad := range []map[string]any{{"log_days": -1}, {"log_days": 5000}, {"keep_per_task": 2000}, {"max_total_bytes": -5}, {"log_days": "x"}} {
		if _, err := callStorage(t, a, "storage.settings", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"log_days": 7, "max_total_bytes": 2 << 30}); err != nil {
		t.Fatal(err)
	}
	got := a.RetentionSettings(ctx)
	if got.LogDays != 7 || got.MaxTotalBytes != 2<<30 || got.ArtifactDays != DefaultRetention.ArtifactDays || got.KeepPerTask != DefaultRetention.KeepPerTask {
		t.Fatalf("partial update %+v", got)
	}
	// A corrupt stored value falls back to the defaults instead of deleting everything.
	_ = a.Auth.SetSetting(ctx, setRetention, `{"log_days":-3}`)
	if got := a.RetentionSettings(ctx); got != DefaultRetention {
		t.Fatalf("corrupt fallback %+v", got)
	}
}

func TestAutomaticCleanupUsesStoredPolicy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	logs := filepath.Join(a.Cfg.DataDir, "runs")
	writeOld(t, filepath.Join(logs, "r_a.log"), 10, 10*24*time.Hour)
	a.retentionTick(ctx, time.Now()) // default 30 days: nothing to do, nothing recorded
	if v, _ := a.StorageUsage(ctx, time.Now()); v.Last != nil || v.Parts["logs"].Count != 1 {
		t.Fatalf("default policy deleted or recorded: %+v", v)
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"log_days": 7, "keep_per_task": 0}); err != nil {
		t.Fatal(err)
	}
	a.retentionTick(ctx, time.Now())
	v, _ := a.StorageUsage(ctx, time.Now())
	if v.Parts["logs"].Count != 0 || v.Last == nil || !v.Last.Auto || v.Last.FreedBytes != 10 {
		t.Fatalf("auto cleanup %+v last %+v", v.Parts, v.Last)
	}
}

func TestShrinkDatabase(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.DB.Exec(`CREATE TABLE junk(b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		_, _ = a.DB.Exec(`INSERT INTO junk VALUES(?)`, make([]byte, 64<<10))
	}
	_, _ = a.DB.Exec(`DELETE FROM junk`)
	_, _ = a.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	v, _ := a.StorageUsage(ctx, time.Now())
	if v.Reclaimable < 1<<20 {
		t.Fatalf("expected reclaimable space, got %d", v.Reclaimable)
	}
	b, err := callStorage(t, a, "storage.vacuum", nil)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Saved   int64       `json:"saved_bytes"`
		Storage StorageView `json:"storage"`
	}
	_ = json.Unmarshal(b, &res)
	if res.Saved < 1<<20 || res.Storage.Reclaimable >= 1<<20 {
		t.Fatalf("vacuum %s", b)
	}
}

// Finished runs' logs are gzipped by the periodic sweep; usage reports them, retention
// still counts their age and deletes them by run id.
func TestRetentionTickPacksFinishedLogs(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	logs := filepath.Join(a.Cfg.DataDir, "runs")
	day := 24 * time.Hour
	text := func(p string, age time.Duration) {
		t.Helper()
		os.MkdirAll(logs, 0o755)
		os.WriteFile(p, []byte(strings.Repeat("[09:00:00] 读取 README.md → ok\n", 2000)), 0o600)
		ts := time.Now().Add(-age)
		os.Chtimes(p, ts, ts)
	}
	text(filepath.Join(logs, "r_done.log"), 2*day)
	text(filepath.Join(logs, "r_old.log"), 40*day)
	text(filepath.Join(logs, "r_run.log"), 2*day)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('r_run','running',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"keep_per_task": 0}); err != nil {
		t.Fatal(err)
	}
	a.retentionTick(ctx, time.Now())
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(logs, n)); return err == nil }
	if !exists("r_done.log.gz") || exists("r_done.log") || !exists("r_run.log") || exists("r_old.log") || exists("r_old.log.gz") {
		ents, _ := os.ReadDir(logs)
		t.Fatalf("after tick: %v", ents)
	}
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lp := v.Parts["logs"]
	if lp.Count != 2 || lp.Packed != 1 || lp.PackedRaw <= lp.PackedOn || lp.PackedOn <= 0 {
		t.Fatalf("logs part %+v", lp)
	}
	// Retention ages the packed log like the plain one and finds it by run id.
	plan, err := a.planRetention(ctx, RetentionPolicy{LogDays: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range plan.del {
		if f.runID == "r_done" && f.packed {
			found = true
		}
	}
	if !found {
		t.Fatalf("packed log not planned for deletion: %+v", plan.del)
	}
}
