// Package cost enforces run/task/global budgets and records a cost ledger.
package cost

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

const (
	BucketProduction   = "production"
	BucketCanary       = "canary"
	BucketOptimization = "optimization"
	BucketDryRun       = "dry_run"
	BucketAssist       = "assist" // amendments / consolidation
)

// Budget describes a spending limit.
type Budget struct {
	ID        string
	Scope     string // global|task|bucket
	ScopeID   string
	Period    string // run|day
	MaxUSD    sql.NullFloat64
	MaxTokens sql.NullInt64
	Action    string
	WarnAtPct int
	Bucket    string // optional filter; empty = all buckets for scope
}

// Service manages budgets and ledger.
type Service struct {
	DB  *sql.DB
	Log *slog.Logger
	// Loc is the timezone whose midnight resets daily budgets (default time.Local).
	Loc *time.Location
	// Now is overridable for tests.
	Now func() time.Time
}

func New(db *sql.DB, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{DB: db, Log: log}
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Service) loc() *time.Location {
	if s.Loc != nil {
		return s.Loc
	}
	return time.Local
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Today returns the current local day (YYYY-MM-DD) in the configured timezone.
func (s *Service) Today() string { return s.now().In(s.loc()).Format("2006-01-02") }

// DayBounds returns [start, end) of the local calendar day containing t in loc.
// It handles DST days (23h/25h) correctly.
func DayBounds(t time.Time, loc *time.Location) (time.Time, time.Time) {
	lt := t.In(loc)
	start := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	end := time.Date(lt.Year(), lt.Month(), lt.Day()+1, 0, 0, 0, 0, loc)
	return start, end
}

// ResetsAt returns the next local midnight when daily budgets reset.
func (s *Service) ResetsAt() time.Time {
	_, end := DayBounds(s.now(), s.loc())
	return end
}

// dayRange converts a local day string into UTC RFC3339 bounds for created_at.
func (s *Service) dayRange(day string) (string, string, error) {
	d, err := time.ParseInLocation("2006-01-02", day, s.loc())
	if err != nil {
		return "", "", fmt.Errorf("bad day %q", day)
	}
	st, en := DayBounds(d, s.loc())
	return st.UTC().Format(time.RFC3339), en.UTC().Format(time.RFC3339), nil
}

// DayRange is the exported form of dayRange.
func (s *Service) DayRange(day string) (string, string, error) { return s.dayRange(day) }

// EnsureDefaults seeds global daily and assist/optimization daily budgets if missing.
func (s *Service) EnsureDefaults(ctx context.Context) error {
	defaults := []Budget{
		{Scope: "global", Period: "day", MaxUSD: sql.NullFloat64{Float64: 5, Valid: true}, MaxTokens: sql.NullInt64{Int64: 2_000_000, Valid: true}, Action: "pause_notify", WarnAtPct: 80},
		{Scope: "bucket", ScopeID: BucketAssist, Period: "day", MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}, Action: "pause_notify", WarnAtPct: 80, Bucket: BucketAssist},
		{Scope: "bucket", ScopeID: BucketOptimization, Period: "day", MaxUSD: sql.NullFloat64{Float64: 1, Valid: true}, Action: "pause_notify", WarnAtPct: 80, Bucket: BucketOptimization},
		{Scope: "bucket", ScopeID: BucketCanary, Period: "day", MaxUSD: sql.NullFloat64{Float64: 2, Valid: true}, Action: "pause_notify", WarnAtPct: 80, Bucket: BucketCanary},
	}
	for _, b := range defaults {
		var n int
		_ = s.DB.QueryRowContext(ctx, `
SELECT COUNT(1) FROM budgets WHERE scope=? AND scope_id=? AND period=? AND bucket=?`,
			b.Scope, b.ScopeID, b.Period, b.Bucket).Scan(&n)
		if n > 0 {
			continue
		}
		now := nowUTC()
		_, err := s.DB.ExecContext(ctx, `
INSERT INTO budgets(id, scope, scope_id, period, max_usd, max_tokens, action, warn_at_pct, bucket, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id.New("bud"), b.Scope, b.ScopeID, b.Period, b.MaxUSD, b.MaxTokens, b.Action, b.WarnAtPct, b.Bucket, now, now)
		if err != nil {
			return err
		}
	}
	return nil
}

// CheckResult is the outcome of a pre-run budget check.
type CheckResult struct {
	OK           bool
	Reason       string
	Warn         bool
	WarnDetail   string
	RunMaxUSD    float64 // suggested --max-budget-usd for this run
	BreachScope  string
	BreachAction string
}

// CheckBeforeRun validates global/task/bucket daily caps and returns per-run max.
func (s *Service) CheckBeforeRun(ctx context.Context, taskID, bucket string, runMaxUSD float64) (CheckResult, error) {
	if bucket == "" {
		bucket = BucketProduction
	}
	if runMaxUSD <= 0 {
		runMaxUSD = 0.05
	}
	out := CheckResult{OK: true, RunMaxUSD: runMaxUSD}

	// Per-run budget row (scope=task period=run) can tighten runMaxUSD
	if taskID != "" {
		if b, err := s.findBudget(ctx, "task", taskID, "run", ""); err == nil && b.MaxUSD.Valid && b.MaxUSD.Float64 > 0 {
			if b.MaxUSD.Float64 < out.RunMaxUSD {
				out.RunMaxUSD = b.MaxUSD.Float64
			}
		}
	}

	checks := []struct {
		scope, scopeID, period, buck string
	}{
		{"global", "", "day", ""},
		{"bucket", bucket, "day", bucket},
	}
	if taskID != "" {
		checks = append(checks, struct{ scope, scopeID, period, buck string }{"task", taskID, "day", ""})
	}

	day := s.Today()
	for _, c := range checks {
		b, err := s.findBudget(ctx, c.scope, c.scopeID, c.period, c.buck)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return out, err
		}
		usedUSD, usedTok, err := s.sumDay(ctx, day, c.scope, c.scopeID, c.buck)
		if err != nil {
			return out, err
		}
		if b.MaxUSD.Valid && b.MaxUSD.Float64 > 0 {
			pct := usedUSD / b.MaxUSD.Float64 * 100
			if usedUSD >= b.MaxUSD.Float64 {
				out.OK = false
				out.Reason = fmt.Sprintf("%s/%s daily USD cap reached (%.4f/%.4f)", c.scope, c.scopeID, usedUSD, b.MaxUSD.Float64)
				out.BreachScope = c.scope
				out.BreachAction = b.Action
				return out, nil
			}
			if b.WarnAtPct > 0 && pct >= float64(b.WarnAtPct) {
				out.Warn = true
				out.WarnDetail = fmt.Sprintf("%s spend at %.0f%% of daily USD cap", c.scope, pct)
			}
			remain := b.MaxUSD.Float64 - usedUSD
			if remain < out.RunMaxUSD {
				out.RunMaxUSD = remain
			}
		}
		if b.MaxTokens.Valid && b.MaxTokens.Int64 > 0 && usedTok >= b.MaxTokens.Int64 {
			out.OK = false
			out.Reason = fmt.Sprintf("%s/%s daily token cap reached (%d/%d)", c.scope, c.scopeID, usedTok, b.MaxTokens.Int64)
			out.BreachScope = c.scope
			out.BreachAction = b.Action
			return out, nil
		}
	}
	if out.RunMaxUSD <= 0 {
		out.OK = false
		out.Reason = "remaining daily budget is zero"
		out.BreachAction = "pause_notify"
	}
	return out, nil
}

func (s *Service) findBudget(ctx context.Context, scope, scopeID, period, bucket string) (Budget, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, scope, scope_id, period, max_usd, max_tokens, action, warn_at_pct, bucket
FROM budgets WHERE scope=? AND scope_id=? AND period=? AND bucket=?`, scope, scopeID, period, bucket)
	var b Budget
	err := row.Scan(&b.ID, &b.Scope, &b.ScopeID, &b.Period, &b.MaxUSD, &b.MaxTokens, &b.Action, &b.WarnAtPct, &b.Bucket)
	return b, err
}

func (s *Service) sumDay(ctx context.Context, day, scope, scopeID, bucket string) (usd float64, tokens int64, err error) {
	st, en, err := s.dayRange(day)
	if err != nil {
		return 0, 0, err
	}
	q := `SELECT COALESCE(SUM(usd),0), COALESCE(SUM(tokens),0) FROM cost_ledger WHERE created_at >= ? AND created_at < ?`
	args := []any{st, en}
	if scope == "task" && scopeID != "" {
		q += ` AND task_id=?`
		args = append(args, scopeID)
	}
	if bucket != "" {
		q += ` AND bucket=?`
		args = append(args, bucket)
	}
	err = s.DB.QueryRowContext(ctx, q, args...).Scan(&usd, &tokens)
	return
}

// Record writes a ledger row after a run finishes.
func (s *Service) Record(ctx context.Context, runID, taskID, bucket string, usd float64, inTok, outTok int) error {
	if bucket == "" {
		bucket = BucketProduction
	}
	tokens := inTok + outTok
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO cost_ledger(run_id, bucket, task_id, usd, tokens, input_tokens, output_tokens, day, month, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(run_id) DO UPDATE SET usd=excluded.usd, tokens=excluded.tokens,
  input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens`,
		runID, bucket, nullEmpty(taskID), usd, tokens, inTok, outTok, s.now().In(s.loc()).Format("2006-01-02"), s.now().In(s.loc()).Format("2006-01"), s.now().UTC().Format(time.RFC3339))
	return err
}

func nullEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LogBreach records a budget event and a notification (log channel).
func (s *Service) LogBreach(ctx context.Context, scope, scopeID, bucket, kind, detail, action string) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO budget_events(id, at, scope, scope_id, bucket, kind, detail, action_taken)
VALUES(?,?,?,?,?,?,?,?)`, id.New("bev"), nowUTC(), scope, scopeID, bucket, kind, detail, action)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO notifications(id, event, ref, body, status, created_at)
VALUES(?,?,?,?, 'logged', ?)`, id.New("ntf"), "budget_"+kind, scope+":"+scopeID, detail, nowUTC())
	if err != nil {
		return err
	}
	s.Log.Warn("budget event", "scope", scope, "scope_id", scopeID, "kind", kind, "detail", detail, "action", action)
	return nil
}

// PauseTask marks a task paused for budget reasons.
func (s *Service) PauseTask(ctx context.Context, taskID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE tasks SET status='paused', updated_at=? WHERE id=?`, nowUTC(), taskID)
	_ = reason
	return err
}

// SpendSummary aggregates spend for dashboards / agentctl.
type SpendSummary struct {
	Day         string             `json:"day"`
	Timezone    string             `json:"timezone"`
	ResetsAt    string             `json:"resets_at"`
	TotalUSD    float64            `json:"total_usd"`
	TotalTokens int64              `json:"total_tokens"`
	ByBucket    map[string]float64 `json:"by_bucket_usd"`
	ByBucketTok map[string]int64   `json:"by_bucket_tokens"`
	Budgets     []BudgetView       `json:"budgets"`
}

type BudgetView struct {
	Scope     string  `json:"scope"`
	ScopeID   string  `json:"scope_id"`
	Period    string  `json:"period"`
	Bucket    string  `json:"bucket"`
	MaxUSD    float64 `json:"max_usd"`
	MaxTokens int64   `json:"max_tokens"`
	UsedUSD   float64 `json:"used_usd"`
	UsedTok   int64   `json:"used_tokens"`
}

// Summary returns today's spend and budget usage.
func (s *Service) Summary(ctx context.Context, day string) (SpendSummary, error) {
	if day == "" {
		day = s.Today()
	}
	out := SpendSummary{Day: day, Timezone: s.loc().String(), ResetsAt: s.ResetsAt().UTC().Format(time.RFC3339),
		ByBucket: map[string]float64{}, ByBucketTok: map[string]int64{}}
	st, en, err := s.dayRange(day)
	if err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT bucket, COALESCE(SUM(usd),0), COALESCE(SUM(tokens),0) FROM cost_ledger WHERE created_at >= ? AND created_at < ? GROUP BY bucket`, st, en)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		var u float64
		var t int64
		if err := rows.Scan(&b, &u, &t); err != nil {
			return out, err
		}
		out.ByBucket[b] = u
		out.ByBucketTok[b] = t
		out.TotalUSD += u
		out.TotalTokens += t
	}
	brows, err := s.DB.QueryContext(ctx, `
SELECT scope, scope_id, period, bucket, COALESCE(max_usd,0), COALESCE(max_tokens,0) FROM budgets`)
	if err != nil {
		return out, err
	}
	defer brows.Close()
	var buds []BudgetView
	type raw struct {
		Scope, ScopeID, Period, Bucket string
		MaxUSD                         float64
		MaxTokens                      int64
	}
	var list []raw
	for brows.Next() {
		var r raw
		if err := brows.Scan(&r.Scope, &r.ScopeID, &r.Period, &r.Bucket, &r.MaxUSD, &r.MaxTokens); err != nil {
			return out, err
		}
		list = append(list, r)
	}
	brows.Close()
	for _, r := range list {
		usedUSD, usedTok, _ := s.sumDay(ctx, day, r.Scope, r.ScopeID, r.Bucket)
		buds = append(buds, BudgetView{
			Scope: r.Scope, ScopeID: r.ScopeID, Period: r.Period, Bucket: r.Bucket,
			MaxUSD: r.MaxUSD, MaxTokens: r.MaxTokens, UsedUSD: usedUSD, UsedTok: usedTok,
		})
	}
	out.Budgets = buds
	return out, nil
}

// UpsertBudget creates or updates a budget row.
func (s *Service) UpsertBudget(ctx context.Context, b Budget) error {
	now := nowUTC()
	var existing string
	err := s.DB.QueryRowContext(ctx, `
SELECT id FROM budgets WHERE scope=? AND scope_id=? AND period=? AND bucket=?`,
		b.Scope, b.ScopeID, b.Period, b.Bucket).Scan(&existing)
	if err == sql.ErrNoRows {
		id0 := id.New("bud")
		_, err = s.DB.ExecContext(ctx, `
INSERT INTO budgets(id, scope, scope_id, period, max_usd, max_tokens, action, warn_at_pct, bucket, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id0, b.Scope, b.ScopeID, b.Period, b.MaxUSD, b.MaxTokens, nullDefault(b.Action, "pause_notify"),
			nullInt(b.WarnAtPct, 80), b.Bucket, now, now)
		return err
	}
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `
UPDATE budgets SET max_usd=?, max_tokens=?, action=?, warn_at_pct=?, updated_at=? WHERE id=?`,
		b.MaxUSD, b.MaxTokens, nullDefault(b.Action, "pause_notify"), nullInt(b.WarnAtPct, 80), now, existing)
	return err
}

func nullDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
func nullInt(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}

// Location returns the timezone used for daily budget boundaries.
func (s *Service) Location() *time.Location { return s.loc() }
