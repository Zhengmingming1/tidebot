package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/diskspace"
)

// Under the minimum free space a run is not started: it ends as skipped with the
// figures, without a retry, a breaker strike or a "failed" notice; the low-disk
// notice goes out instead. Freeing space (or turning the check off) lets the next
// run start. An unknown disk never blocks.
func TestDiskFloorSkipsRuns(t *testing.T) {
	a := newTestApp(t)
	a.retryScale = 0.0001
	ctx := context.Background()
	task, err := a.EnsureTask(ctx, "daily", "do it", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	free, total := uint64(300<<20), 64*gib // under the 500 MB default
	fakeDisk(a, &free, &total)
	ch := a.Events.Subscribe(64)

	for i := 0; i < 4; i++ { // more than the breaker's 3
		a.runWithPolicy(ctx, task, "cron", "production", 0)
	}
	var n int
	var msg string
	_ = a.DB.QueryRow(`SELECT COUNT(*), MAX(error_message) FROM runs WHERE task_id=? AND status='skipped' AND started_at IS NULL`, task.ID).Scan(&n, &msg)
	if n != 4 || msg != "skipped: disk full: 314572800/524288000" {
		t.Fatalf("skipped runs %d %q", n, msg)
	}
	var other int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status<>'skipped'`, task.ID).Scan(&other)
	tk, _ := a.Store.GetTask(ctx, task.ID)
	if other != 0 || tk.Status != "active" || a.Policy.Get(ctx, task.ID).ConsecutiveFailures != 0 {
		t.Fatalf("retried or struck: other=%d status=%s fails=%d", other, tk.Status, a.Policy.Get(ctx, task.ID).ConsecutiveFailures)
	}
	low, failed := 0, 0
	for len(ch) > 0 {
		switch e := <-ch; e.Kind {
		case EventDiskLow:
			low++
		case EventRunFailed, EventBreakerTripped:
			failed++
		}
	}
	if low != 1 || failed != 0 {
		t.Fatalf("events: disk_low=%d failed=%d", low, failed)
	}
	if d := a.lastDisk(); d == nil || d.Free != free {
		t.Fatalf("cached disk not refreshed: %+v", d)
	}

	// Space freed: the next run starts.
	free = 2 * gib
	a.runWithPolicy(ctx, task, "cron", "production", 0)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id=? AND status='succeeded'`, task.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("run after freeing space: %d succeeded", n)
	}

	// Check off (0): runs start even on a nearly full disk.
	free = 10 << 20
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"min_free_mb": 0}); err != nil {
		t.Fatal(err)
	}
	if err := a.diskStartCheck(ctx); err != nil {
		t.Fatalf("check off: %v", err)
	}
	// Unknown disk: never blocks.
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"min_free_mb": 1024}); err != nil {
		t.Fatal(err)
	}
	a.disk.measure = func(string) (diskspace.Info, error) { return diskspace.Info{}, diskspace.ErrUnsupported }
	if err := a.diskStartCheck(ctx); err != nil {
		t.Fatalf("unknown disk: %v", err)
	}
}

func TestDiskFloorSetting(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	free, total := uint64(700<<20), 64*gib
	fakeDisk(a, &free, &total)

	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil || v.MinFreeMB != DefaultMinFreeMB || v.DefaultMinFreeMB != DefaultMinFreeMB || v.Blocked {
		t.Fatalf("default view %+v %v", v, err)
	}
	raw, err := callStorage(t, a, "storage.settings", map[string]any{"min_free_mb": 1024})
	if err != nil {
		t.Fatal(err)
	}
	var got StorageView
	_ = json.Unmarshal(raw, &got)
	if got.MinFreeMB != 1024 || !got.Blocked || got.Disk == nil || !got.Disk.Low {
		t.Fatalf("after 1 GB: %+v", got)
	}
	// Retention is untouched by a min-free-only update.
	if got.Policy != DefaultRetention {
		t.Fatalf("policy changed: %+v", got.Policy)
	}
	for _, bad := range []any{-1, 1025, "500"} {
		if _, err := callStorage(t, a, "storage.settings", map[string]any{"min_free_mb": bad}); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if a.MinFreeMB(ctx) != 1024 {
		t.Fatal("bad values must not change the stored minimum")
	}
	// A corrupt stored value falls back to the default.
	_ = a.saveJSONSetting(ctx, setDiskMinFree, 99999)
	if a.MinFreeMB(ctx) != DefaultMinFreeMB {
		t.Fatal("out-of-range stored value should fall back")
	}
}
