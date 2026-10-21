package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/diskspace"
)

// Low-disk warning for the volume holding tidebot's data (database, run logs,
// artifacts). A full disk makes SQLite writes fail mid-run, so we say so early.
//
// The shape follows Coolify's ServerStorageCheckJob + HighDiskUsage notification:
// warn when crossing a threshold, re-warn at most once a day while it stays low
// (throttle), and only arm again after usage recovers past a margin (hysteresis),
// so a disk hovering at the line doesn't produce a notification every check.

const (
	EventDiskLow      = "disk_low"
	setDiskAlert      = "disk.alerted_at"
	diskCheckEvery    = 15 * time.Minute
	diskRealertAfter  = 24 * time.Hour
	diskMinThreshold  = uint64(1) << 30 // 1 GiB
	diskMaxThreshold  = uint64(5) << 30 // 5 GiB
	diskThresholdPart = 20              // 1/20 = 5% of the volume
)

// DiskStatus is the data volume as Settings › Data and the tray see it.
type DiskStatus struct {
	diskspace.Info
	Threshold uint64 `json:"threshold_bytes"`
	Low       bool   `json:"low"`
}

// diskThreshold is 5% of the volume, kept between 1 and 5 GiB: small VPS disks
// warn at 1 GiB, laptops and big servers at 5 GiB.
func diskThreshold(total uint64) uint64 {
	t := total / diskThresholdPart
	if t < diskMinThreshold {
		t = diskMinThreshold
	}
	if t > diskMaxThreshold {
		t = diskMaxThreshold
	}
	return t
}

type diskWatch struct {
	mu      sync.Mutex
	low     bool      // currently in a low episode (armed until recovered)
	alerted time.Time // last warning sent
	loaded  bool
	last    *DiskStatus
	measure func(string) (diskspace.Info, error) // tests swap this
}

// diskPaths are the folders whose volume matters; the database may live elsewhere.
func (a *App) diskPaths() []string {
	ps := []string{a.Cfg.DataDir}
	if d := filepath.Dir(a.Cfg.DBPath); a.Cfg.DBPath != "" && filepath.Clean(d) != filepath.Clean(a.Cfg.DataDir) {
		ps = append(ps, d)
	}
	return ps
}

// DiskStatus measures the data volume(s) and returns the tightest one.
// ok is false when the platform can't tell (the UI then shows nothing).
func (a *App) DiskStatus() (DiskStatus, bool) {
	measure := a.disk.measure
	if measure == nil {
		measure = diskspace.Of
	}
	var best DiskStatus
	found := false
	for _, p := range a.diskPaths() {
		in, err := measure(p)
		if err != nil || in.Total == 0 {
			continue
		}
		if !found || in.Free < best.Free {
			best, found = DiskStatus{Info: in}, true
		}
	}
	if !found {
		return DiskStatus{}, false
	}
	best.Threshold = diskThreshold(best.Total)
	best.Low = best.Free < best.Threshold
	return best, true
}

// diskTick checks once and emits EventDiskLow when a warning is due.
func (a *App) diskTick(ctx context.Context, now time.Time) {
	st, ok := a.DiskStatus()
	w := &a.disk
	w.mu.Lock()
	defer w.mu.Unlock()
	if !ok {
		w.last = nil
		return
	}
	w.last = &st
	if !w.loaded {
		w.loaded = true
		var at string
		a.loadJSONSetting(ctx, setDiskAlert, &at)
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			w.alerted, w.low = t, true // survive restarts: no repeat within a day
		}
	}
	if !st.Low {
		// Re-arm only after a clear recovery (threshold + 50%).
		if w.low && st.Free >= st.Threshold+st.Threshold/2 {
			w.low, w.alerted = false, time.Time{}
			_ = a.saveJSONSetting(ctx, setDiskAlert, "")
		}
		return
	}
	if w.low && now.Sub(w.alerted) < diskRealertAfter {
		return
	}
	w.low, w.alerted = true, now
	_ = a.saveJSONSetting(ctx, setDiskAlert, now.UTC().Format(time.RFC3339))
	a.Log.Warn("disk space low", "free_bytes", st.Free, "total_bytes", st.Total)
	a.Events.Emit(Event{Kind: EventDiskLow, Detail: fmt.Sprintf("%d/%d", st.Free, st.Total)})
}

// lastDisk is the most recent measurement (for the tray, which polls often).
func (a *App) lastDisk() *DiskStatus {
	a.disk.mu.Lock()
	defer a.disk.mu.Unlock()
	return a.disk.last
}

func (a *App) runDiskWatch(ctx context.Context) {
	t := time.NewTicker(diskCheckEvery)
	defer t.Stop()
	for {
		a.diskTick(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// fmtBytesShort is a compact size for notifications ("812 MB", "3.4 GB").
func fmtBytesShort(n uint64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%d MB", n/(k*k))
	default:
		return fmt.Sprintf("%d KB", n/k)
	}
}
