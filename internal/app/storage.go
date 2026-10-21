package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Storage settings and usage for Settings › Data. The idea comes from Uptime Kuma's
// "Monitor History" settings (keep N days, 0 = forever; show the database size next to
// a "shrink database" button): users decide how much history to keep, see what it
// costs on disk, and can reclaim it without a terminal.

const (
	setRetention     = "retention"
	setRetentionLast = "retention.last"
	maxRetentionDays = 3650
	maxKeepPerTask   = 1000
	maxCapBytes      = int64(1000) << 30
)

// RetentionSettings is the stored policy, with DefaultRetention for anything unset.
func (a *App) RetentionSettings(ctx context.Context) RetentionPolicy {
	p := DefaultRetention
	a.loadJSONSetting(ctx, setRetention, &p)
	if validRetention(p) != nil {
		return DefaultRetention
	}
	return p
}

func validRetention(p RetentionPolicy) error {
	switch {
	case p.LogDays < 0 || p.LogDays > maxRetentionDays, p.ArtifactDays < 0 || p.ArtifactDays > maxRetentionDays:
		return errors.New("days must be 0..3650 (0 = keep forever)")
	case p.KeepPerTask < 0 || p.KeepPerTask > maxKeepPerTask:
		return errors.New("keep_per_task must be 0..1000")
	case p.MaxTotalBytes < 0 || p.MaxTotalBytes > maxCapBytes:
		return errors.New("max_total_bytes must be 0..1000 GB (0 = no cap)")
	}
	return nil
}

// CleanupRecord is the last cleanup (automatic or from the WebUI).
type CleanupRecord struct {
	At           string `json:"at"`
	Auto         bool   `json:"auto"`
	DeletedFiles int    `json:"deleted_files"`
	FreedBytes   int64  `json:"freed_bytes"`
}

func (a *App) runCleanup(ctx context.Context, now time.Time, auto bool) (RetentionReport, error) {
	rep, err := a.ApplyRetention(ctx, a.RetentionSettings(ctx), now)
	if err != nil {
		return rep, err
	}
	if !auto || rep.DeletedFiles > 0 {
		_ = a.saveJSONSetting(ctx, setRetentionLast, CleanupRecord{At: now.UTC().Format(time.RFC3339), Auto: auto, DeletedFiles: rep.DeletedFiles, FreedBytes: rep.FreedBytes})
	}
	return rep, nil
}

func (a *App) retentionTick(ctx context.Context, now time.Time) {
	if rep, err := a.runCleanup(ctx, now, true); err != nil {
		a.Log.Warn("retention", "error", err)
	} else if rep.DeletedFiles > 0 {
		a.Log.Info("retention", "deleted", rep.DeletedFiles, "freed_bytes", rep.FreedBytes)
	}
}

// UsagePart is one slice of the storage bar.
type UsagePart struct {
	Bytes int64 `json:"bytes"`
	Count int   `json:"count"`
}

// StorageView is what Settings › Data shows.
type StorageView struct {
	Policy      RetentionPolicy      `json:"policy"`
	Defaults    RetentionPolicy      `json:"defaults"`
	Parts       map[string]UsagePart `json:"parts"` // logs | artifacts | database | backups
	TotalBytes  int64                `json:"total_bytes"`
	Reclaimable int64                `json:"db_reclaimable"` // free pages VACUUM would return
	Preview     struct {
		Files int   `json:"files"`
		Bytes int64 `json:"bytes"`
	} `json:"preview"` // what "Clean up now" would delete under the current policy
	Last *CleanupRecord `json:"last,omitempty"`
}

// StorageUsage measures the data folder. It never lists file names or paths.
func (a *App) StorageUsage(ctx context.Context, now time.Time) (StorageView, error) {
	v := StorageView{Policy: a.RetentionSettings(ctx), Defaults: DefaultRetention, Parts: map[string]UsagePart{}}
	for _, f := range a.runFiles() {
		k := "logs"
		if f.kind == "artifact" {
			k = "artifacts"
		}
		p := v.Parts[k]
		p.Bytes += f.size
		p.Count++
		v.Parts[k] = p
	}
	var db UsagePart
	for _, suf := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(a.Cfg.DBPath + suf); err == nil {
			db.Bytes += fi.Size()
			if suf == "" {
				db.Count = 1
			}
		}
	}
	v.Parts["database"] = db
	if ents, err := os.ReadDir(a.backupDir()); err == nil {
		var b UsagePart
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
				b.Bytes += fi.Size()
				b.Count++
			}
		}
		v.Parts["backups"] = b
	}
	for _, p := range v.Parts {
		v.TotalBytes += p.Bytes
	}
	var free, page int64
	if a.DB.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free) == nil && a.DB.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&page) == nil {
		v.Reclaimable = free * page
	}
	plan, err := a.planRetention(ctx, v.Policy, now)
	if err != nil {
		return v, err
	}
	v.Preview.Files, v.Preview.Bytes = len(plan.del), plan.FreeBytes
	var last CleanupRecord
	a.loadJSONSetting(ctx, setRetentionLast, &last)
	if last.At != "" {
		v.Last = &last
	}
	return v, nil
}

// ShrinkDatabase runs VACUUM and truncates the WAL; returns bytes saved on disk.
func (a *App) ShrinkDatabase(ctx context.Context) (int64, error) {
	size := func() int64 {
		var n int64
		for _, suf := range []string{"", "-wal"} {
			if fi, err := os.Stat(a.Cfg.DBPath + suf); err == nil {
				n += fi.Size()
			}
		}
		return n
	}
	before := size()
	if _, err := a.DB.ExecContext(ctx, `VACUUM`); err != nil {
		return 0, err
	}
	_, _ = a.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	saved := before - size()
	if saved < 0 {
		saved = 0
	}
	return saved, nil
}

func (a *App) storageRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"storage.get": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.StorageUsage(ctx, time.Now()) },
		// Partial update: only the fields sent change. Returns the fresh view so the
		// "clean up now" preview follows the new policy immediately.
		"storage.settings": func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode[struct {
				LogDays       *int   `json:"log_days"`
				ArtifactDays  *int   `json:"artifact_days"`
				KeepPerTask   *int   `json:"keep_per_task"`
				MaxTotalBytes *int64 `json:"max_total_bytes"`
			}](raw)
			if err != nil {
				return nil, errors.New("bad request")
			}
			p := a.RetentionSettings(ctx)
			if in.LogDays != nil {
				p.LogDays = *in.LogDays
			}
			if in.ArtifactDays != nil {
				p.ArtifactDays = *in.ArtifactDays
			}
			if in.KeepPerTask != nil {
				p.KeepPerTask = *in.KeepPerTask
			}
			if in.MaxTotalBytes != nil {
				p.MaxTotalBytes = *in.MaxTotalBytes
			}
			if err := validRetention(p); err != nil {
				return nil, err
			}
			if err := a.saveJSONSetting(ctx, setRetention, p); err != nil {
				return nil, err
			}
			return a.StorageUsage(ctx, time.Now())
		},
		"storage.cleanup": func(ctx context.Context, _ json.RawMessage) (any, error) {
			rep, err := a.runCleanup(ctx, time.Now(), false)
			if err != nil {
				return nil, err
			}
			v, err := a.StorageUsage(ctx, time.Now())
			return map[string]any{"report": rep, "storage": v}, err
		},
		"storage.vacuum": func(ctx context.Context, _ json.RawMessage) (any, error) {
			saved, err := a.ShrinkDatabase(ctx)
			if err != nil {
				return nil, err
			}
			v, err := a.StorageUsage(ctx, time.Now())
			return map[string]any{"saved_bytes": saved, "storage": v}, err
		},
	}
}
