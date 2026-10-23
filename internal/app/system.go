package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
)

// --- retention ---------------------------------------------------------------

// RetentionPolicy: transcripts 30 d, artifacts 90 d, always keep the newest 20 runs per
// task, never touch runs flagged important, and cap logs+artifacts at 10 GB.
type RetentionPolicy struct {
	LogDays       int   `json:"log_days"`
	ArtifactDays  int   `json:"artifact_days"`
	KeepPerTask   int   `json:"keep_per_task"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
}

// DefaultRetention is the suggested policy.
var DefaultRetention = RetentionPolicy{LogDays: 30, ArtifactDays: 90, KeepPerTask: 20, MaxTotalBytes: 10 << 30}

// RetentionReport says what was deleted.
type RetentionReport struct {
	DeletedFiles int   `json:"deleted_files"`
	FreedBytes   int64 `json:"freed_bytes"`
	TotalBytes   int64 `json:"total_bytes"`
}

type runFile struct {
	runID string
	path  string
	size  int64
	mod   time.Time
	kind  string // log | artifact
}

// ApplyRetention deletes old transcripts/artifacts. Run metadata rows stay in the DB.
// A zero LogDays/ArtifactDays keeps that kind forever; a zero MaxTotalBytes means no cap.
func (a *App) ApplyRetention(ctx context.Context, p RetentionPolicy, now time.Time) (RetentionReport, error) {
	plan, err := a.planRetention(ctx, p, now)
	if err != nil {
		return RetentionReport{}, err
	}
	rep := RetentionReport{TotalBytes: plan.TotalBytes}
	for _, f := range plan.del {
		if os.RemoveAll(f.path) == nil {
			rep.DeletedFiles++
			rep.FreedBytes += f.size
		} else {
			rep.TotalBytes += f.size
		}
	}
	return rep, nil
}

type retentionPlan struct {
	del        []runFile
	FreeBytes  int64
	TotalBytes int64 // logs+artifacts left after the plan runs
}

// planRetention decides what ApplyRetention would delete, without touching anything.
// Important runs, the newest KeepPerTask runs of each task and runs still queued or
// running are never selected.
func (a *App) planRetention(ctx context.Context, p RetentionPolicy, now time.Time) (retentionPlan, error) {
	var plan retentionPlan
	protected := map[string]bool{}
	addIDs := func(q string, args ...any) error {
		rows, err := a.DB.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				protected[r] = true
			}
		}
		return rows.Err()
	}
	if err := addIDs(`SELECT run_id FROM run_flags WHERE important=1`); err != nil {
		return plan, err
	}
	if err := addIDs(`SELECT id FROM runs WHERE status IN ('queued','running')`); err != nil {
		return plan, err
	}
	if p.KeepPerTask > 0 {
		if err := addIDs(`SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY COALESCE(task_id,'') ORDER BY created_at DESC) AS rn FROM runs) WHERE rn <= ?`, p.KeepPerTask); err != nil {
			return plan, err
		}
	}
	files := a.runFiles()
	var kept []runFile
	for _, f := range files {
		days := p.LogDays
		if f.kind == "artifact" {
			days = p.ArtifactDays
		}
		if !protected[f.runID] && days > 0 && now.Sub(f.mod) > time.Duration(days)*24*time.Hour {
			plan.del = append(plan.del, f)
			plan.FreeBytes += f.size
			continue
		}
		kept = append(kept, f)
		plan.TotalBytes += f.size
	}
	if p.MaxTotalBytes > 0 && plan.TotalBytes > p.MaxTotalBytes {
		sort.Slice(kept, func(i, j int) bool { return kept[i].mod.Before(kept[j].mod) })
		for _, f := range kept {
			if plan.TotalBytes <= p.MaxTotalBytes {
				break
			}
			if protected[f.runID] {
				continue
			}
			plan.del = append(plan.del, f)
			plan.FreeBytes += f.size
			plan.TotalBytes -= f.size
		}
	}
	return plan, nil
}

// runFiles lists run transcripts (<data>/runs/<id>.log) and artifact folders
// (<data>/artifacts/<id>/) with their size and newest modification time.
func (a *App) runFiles() []runFile {
	var files []runFile
	logDir := filepath.Join(a.Cfg.DataDir, "runs")
	if ents, err := os.ReadDir(logDir); err == nil {
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
				files = append(files, runFile{runID: strings.TrimSuffix(e.Name(), ".log"), path: filepath.Join(logDir, e.Name()), size: fi.Size(), mod: fi.ModTime(), kind: "log"})
			}
		}
	}
	artDir := filepath.Join(a.Cfg.DataDir, "artifacts")
	if ents, err := os.ReadDir(artDir); err == nil {
		for _, e := range ents {
			size, mod := dirSize(filepath.Join(artDir, e.Name()))
			files = append(files, runFile{runID: e.Name(), path: filepath.Join(artDir, e.Name()), size: size, mod: mod, kind: "artifact"})
		}
	}
	return files
}

func dirSize(root string) (size int64, mod time.Time) {
	_ = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			size += fi.Size()
			if fi.ModTime().After(mod) {
				mod = fi.ModTime()
			}
		}
		return nil
	})
	return size, mod
}

func (a *App) runRetention(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		a.retentionTick(ctx, time.Now())
		_ = db.Optimize(ctx, a.DB)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// --- dashboard & metrics -----------------------------------------------------

// Dashboard is a compact health summary.
type Dashboard struct {
	Version          string         `json:"version"`
	ClaudeVersion    string         `json:"claude_version"`
	Runs24h          map[string]int `json:"runs_24h"`
	Runs7d           map[string]int `json:"runs_7d"`
	SuccessRate7d    float64        `json:"success_rate_7d"`
	CostTodayUSD     float64        `json:"cost_today_usd"`
	Cost7dUSD        float64        `json:"cost_7d_usd"`
	DurationP50MS    int64          `json:"duration_p50_ms"`
	DurationP95MS    int64          `json:"duration_p95_ms"`
	TasksActive      int            `json:"tasks_active"`
	TasksPaused      int            `json:"tasks_paused"`
	PendingConfirms  int            `json:"pending_confirmations"`
	PluginsInstalled int            `json:"plugins_installed"`
	LastBackup       string         `json:"last_backup"`
	DiskBytes        int64          `json:"disk_bytes"`
}

func (a *App) countRuns(ctx context.Context, since time.Time) map[string]int {
	out := map[string]int{}
	rows, err := a.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM runs WHERE created_at >= ? GROUP BY status`, since.UTC().Format(time.RFC3339))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		_ = rows.Scan(&s, &n)
		out[s] = n
	}
	return out
}

// BuildDashboard computes the dashboard.
func (a *App) BuildDashboard(ctx context.Context) Dashboard {
	now := time.Now()
	d := Dashboard{Version: buildinfo.Version, ClaudeVersion: a.claudeVersion(), Runs24h: a.countRuns(ctx, now.Add(-24*time.Hour)), Runs7d: a.countRuns(ctx, now.Add(-7*24*time.Hour))}
	total, ok := 0, d.Runs7d["succeeded"]
	for _, n := range d.Runs7d {
		total += n
	}
	if total > 0 {
		d.SuccessRate7d = float64(ok) / float64(total)
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.Cfg.Location())
	_ = a.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+cost.AmountExpr(a.cachedFX(), a.cachedFX().Display, "")+`),0) FROM cost_ledger WHERE created_at >= ?`, day.UTC().Format(time.RFC3339)).Scan(&d.CostTodayUSD)
	_ = a.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+cost.AmountExpr(a.cachedFX(), a.cachedFX().Display, "")+`),0) FROM cost_ledger WHERE created_at >= ?`, now.Add(-7*24*time.Hour).UTC().Format(time.RFC3339)).Scan(&d.Cost7dUSD)
	var durs []int64
	if rows, err := a.DB.QueryContext(ctx, `SELECT duration_ms FROM runs WHERE duration_ms IS NOT NULL AND created_at >= ?`, now.Add(-7*24*time.Hour).UTC().Format(time.RFC3339)); err == nil {
		for rows.Next() {
			var v int64
			_ = rows.Scan(&v)
			durs = append(durs, v)
		}
		rows.Close()
	}
	if len(durs) > 0 {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		d.DurationP50MS = durs[len(durs)/2]
		d.DurationP95MS = durs[(len(durs)*95)/100]
		if i := (len(durs) * 95) / 100; i >= len(durs) {
			d.DurationP95MS = durs[len(durs)-1]
		}
	}
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE status='active'`).Scan(&d.TasksActive)
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE status='paused'`).Scan(&d.TasksPaused)
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending_confirmations WHERE status='pending'`).Scan(&d.PendingConfirms)
	_ = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugins`).Scan(&d.PluginsInstalled)
	_ = a.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(created_at),'') FROM backups`).Scan(&d.LastBackup)
	_ = filepath.Walk(a.Cfg.DataDir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			d.DiskBytes += fi.Size()
		}
		return nil
	})
	return d
}

// MetricsHandler serves Prometheus text format at /metrics. It requires
// "Authorization: Bearer <token>" where the token is created with metrics.token.
func (a *App) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := a.Secrets.Get("metrics.token")
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if err != nil || tok == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		d := a.BuildDashboard(r.Context())
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		var b strings.Builder
		fmt.Fprintf(&b, "# HELP tidebot_build_info Build information.\n# TYPE tidebot_build_info gauge\ntidebot_build_info{version=%q} 1\n", d.Version)
		b.WriteString("# HELP tidebot_runs_24h Runs in the last 24 hours by status.\n# TYPE tidebot_runs_24h gauge\n")
		keys := make([]string, 0, len(d.Runs24h))
		for k := range d.Runs24h {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "tidebot_runs_24h{status=%q} %d\n", k, d.Runs24h[k])
		}
		gauges := []struct {
			n, h string
			v    float64
		}{
			{"tidebot_success_rate_7d", "Share of succeeded runs over 7 days.", d.SuccessRate7d},
			{"tidebot_cost_today_usd", "Spend today (local day).", d.CostTodayUSD},
			{"tidebot_cost_7d_usd", "Spend over 7 days.", d.Cost7dUSD},
			{"tidebot_run_duration_p50_ms", "Median run duration over 7 days.", float64(d.DurationP50MS)},
			{"tidebot_run_duration_p95_ms", "p95 run duration over 7 days.", float64(d.DurationP95MS)},
			{"tidebot_tasks_active", "Active tasks.", float64(d.TasksActive)},
			{"tidebot_tasks_paused", "Paused tasks.", float64(d.TasksPaused)},
			{"tidebot_pending_confirmations", "Dangerous actions waiting for the owner.", float64(d.PendingConfirms)},
			{"tidebot_disk_bytes", "Bytes used under the data dir.", float64(d.DiskBytes)},
		}
		for _, g := range gauges {
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %g\n", g.n, g.h, g.n, g.n, g.v)
		}
		_, _ = w.Write([]byte(b.String()))
	})
}

// --- Claude Code version tracking -------------------------------------------

// MinClaudeVersion is the oldest Claude Code we test against (stream-json + --settings hooks).
const MinClaudeVersion = "2.0.0"

var claudeVer struct {
	sync.Mutex
	v  string
	at time.Time
}

// claudeVersion runs `claude --version` (cached 10 min).
func (a *App) claudeVersion() string {
	claudeVer.Lock()
	defer claudeVer.Unlock()
	if time.Since(claudeVer.at) < 10*time.Minute {
		return claudeVer.v
	}
	claudeVer.at = time.Now()
	claudeVer.v = ""
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := a.ResolveClaude(ctx, false); r.Version != "" {
		claudeVer.v = r.Version
	} else if bin, env := a.claudeExec(ctx); bin != "" {
		if out, err := execOutputEnv(ctx, env, bin, "--version"); err == nil {
			claudeVer.v = parseClaudeVersion(out)
		}
	}
	return claudeVer.v
}

// parseClaudeVersion extracts "2.1.3" from "2.1.3 (Claude Code)".
func parseClaudeVersion(s string) string {
	for _, f := range strings.Fields(s) {
		if len(f) > 0 && f[0] >= '0' && f[0] <= '9' && strings.Count(f, ".") >= 1 {
			return strings.TrimRight(f, ",")
		}
	}
	return ""
}

// ClaudeCompatible reports whether v is at least MinClaudeVersion ("" = unknown → true).
func ClaudeCompatible(v string) bool {
	if v == "" {
		return true
	}
	return compareSemver(v, MinClaudeVersion) >= 0
}

func compareSemver(x, y string) int {
	px, py := strings.Split(strings.SplitN(strings.TrimPrefix(x, "v"), "-", 2)[0], "."), strings.Split(strings.SplitN(strings.TrimPrefix(y, "v"), "-", 2)[0], ".")
	for i := 0; i < 3; i++ {
		var a, b int
		if i < len(px) {
			fmt.Sscanf(px[i], "%d", &a)
		}
		if i < len(py) {
			fmt.Sscanf(py[i], "%d", &b)
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	return 0
}

// recordRunMeta stores the Claude Code version that executed a run.
func (a *App) recordRunMeta(ctx context.Context, runID string) {
	if runID == "" {
		return
	}
	_, _ = a.DB.ExecContext(ctx, `INSERT INTO run_flags(run_id, claude_version) VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET claude_version=excluded.claude_version`, runID, a.claudeVersion())
}

// --- RPC ---------------------------------------------------------------------

func (a *App) systemRPC() map[string]rpcFunc {
	return map[string]rpcFunc{
		"backup.create": func(ctx context.Context, raw json.RawMessage) (any, error) {
			o, _ := decode[BackupOptions](raw)
			o.Kind = "manual"
			p, err := a.CreateBackup(ctx, o)
			return map[string]string{"path": p}, err
		},
		"backup.list": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.ListBackups(ctx) },
		"retention.run": func(ctx context.Context, _ json.RawMessage) (any, error) {
			return a.ApplyRetention(ctx, a.RetentionSettings(ctx), time.Now())
		},
		"runs.mark_important": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				RunID     string `json:"run_id"`
				Important bool   `json:"important"`
			}](raw)
			v := 0
			if p.Important {
				v = 1
			}
			_, err := a.DB.ExecContext(ctx, `INSERT INTO run_flags(run_id, important) VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET important=excluded.important`, p.RunID, v)
			return nil, err
		},
		"dashboard": func(ctx context.Context, _ json.RawMessage) (any, error) { return a.BuildDashboard(ctx), nil },
		"metrics.token": func(ctx context.Context, _ json.RawMessage) (any, error) {
			tok := randomToken()
			if err := a.Secrets.Set("metrics.token", tok); err != nil {
				return nil, err
			}
			return map[string]string{"token": tok, "note": "shown once; send as Authorization: Bearer"}, nil
		},
		"claude.version": func(ctx context.Context, _ json.RawMessage) (any, error) {
			v := a.claudeVersion()
			return map[string]any{"version": v, "min_supported": MinClaudeVersion, "compatible": ClaudeCompatible(v)}, nil
		},
		"update.check": func(ctx context.Context, raw json.RawMessage) (any, error) {
			if p, _ := decode[struct {
				Async bool `json:"async"`
			}](raw); p.Async { // WebUI: answer at once, then poll update.status
				a.StartUpdateCheck()
				return a.UpdateStatus(ctx), nil
			}
			if _, err := a.CheckForUpdate(ctx); err != nil {
				return nil, err
			}
			return a.UpdateStatus(ctx), nil
		},
		"claude.set_path": a.rpcSetClaudePath,
		"update.status":   func(ctx context.Context, _ json.RawMessage) (any, error) { return a.UpdateStatus(ctx), nil },
		"update.settings": a.rpcUpdateSettings,
		"update.apply": func(ctx context.Context, raw json.RawMessage) (any, error) {
			if p, _ := decode[struct {
				Async bool `json:"async"`
			}](raw); p.Async {
				if err := a.StartUpdateApply(); err != nil {
					return nil, err
				}
				return a.UpdateStatus(ctx), nil
			}
			return a.ApplyUpdateNow(ctx)
		},
		"sandbox.run": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct {
				TaskID string `json:"task_id"`
			}](raw)
			return a.StartDryRun(p.TaskID)
		},
		"sandbox.result": func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, _ := decode[struct{ ID string }](raw)
			return a.DryRunResult(p.ID)
		},
	}
}
