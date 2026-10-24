package app

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Minimum free space for starting a run. The low-disk warning (diskwatch.go) says
// "clean up soon"; this is the hard line under which a run is not started at all:
// Claude Code, the run log and SQLite would run out of room half-way, leaving a
// failed run, a cut-off log and possibly broken artifacts. Such a run ends as
// skipped with the reason, is not retried and doesn't count towards pausing the
// task after failures; once space is freed the next run starts normally.
//
// Idea from Sonarr's FreeSpaceSpecification (minimum free space setting, checked
// before each import, accept when free space can't be measured). Same here: an
// unknown disk never blocks a run.

const (
	setDiskMinFree   = "disk.min_free_mb"
	DefaultMinFreeMB = 500
	// maxMinFreeMB stays at the low-disk warning's lowest threshold (1 GiB), so a
	// blocked disk is always also "low": tray, bell and storage card already say so.
	maxMinFreeMB = 1024
)

// errDiskFull: the data disk has less free space than the minimum. The message
// is "disk full: <free bytes>/<minimum bytes>", which the WebUI shows in the
// viewer's language.
var errDiskFull = errors.New("disk full")

// MinFreeMB is the stored minimum (0 = don't check), or the default.
func (a *App) MinFreeMB(ctx context.Context) int {
	v := -1
	a.loadJSONSetting(ctx, setDiskMinFree, &v)
	if validMinFree(v) != nil {
		return DefaultMinFreeMB
	}
	return v
}

func validMinFree(mb int) error {
	if mb < 0 || mb > maxMinFreeMB {
		return errors.New("min_free_mb must be 0..1024 (0 = don't check)")
	}
	return nil
}

// diskStartCheck is every run's Options.StartCheck: measured fresh (one statfs /
// GetDiskFreeSpaceEx call), not from the 15-minute watcher cache.
func (a *App) diskStartCheck(ctx context.Context) error {
	mb := a.MinFreeMB(ctx)
	if mb == 0 {
		return nil
	}
	st, ok := a.DiskStatus()
	floor := uint64(mb) << 20
	if !ok || st.Free >= floor {
		return nil
	}
	a.Log.Warn("run not started: disk almost full", "free_bytes", st.Free, "min_free_bytes", floor)
	// Refresh the cached status (tray, storage card) and send the low-disk notice
	// if it's due; the watcher's own throttle keeps this to one a day.
	a.diskTick(context.WithoutCancel(ctx), time.Now())
	return fmt.Errorf("%w: %d/%d", errDiskFull, st.Free, floor)
}
