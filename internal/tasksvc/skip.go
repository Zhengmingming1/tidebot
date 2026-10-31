package tasksvc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/scheduler"
)

// Skipping one run ("skip next run"): the schedule stays active and only the chosen fire
// time is left out — for a day off or a run you already did by hand — instead of pausing
// the task and remembering to resume it. The idea follows scaleapi/scale-agentex's
// schedules (agentex/src/adapters/temporal/adapter_temporal.py skip_schedule_action:
// a one-off exclusion that must be one of the upcoming action times, undone by
// unskip_schedule_action).

// SkipHorizon: how far ahead a run can be skipped.
const SkipHorizon = 31 * 24 * time.Hour

var (
	// ErrSkipNoSchedule: the task has no schedule, or is not active (a paused task has no
	// next run to skip; resume it or pause it for a while instead).
	ErrSkipNoSchedule = errors.New("only an active scheduled task has runs to skip")
	// ErrSkipNotFireTime: the time is not one of the task's upcoming fire times.
	ErrSkipNotFireTime = errors.New("that time is not one of the task's upcoming runs")
	// ErrSkipTooMany: MaxSkips upcoming runs are skipped already.
	ErrSkipTooMany = fmt.Errorf("at most %d upcoming runs can be skipped; pause the task instead", scheduler.MaxSkips)
)

// SkipResult is the task's schedule after a skip / unskip.
type SkipResult struct {
	At        string   `json:"at"`          // the fire time skipped or put back (RFC 3339 UTC)
	NextRunAt string   `json:"next_run_at"` // the run that happens next
	Skipped   []string `json:"skipped"`     // upcoming fire times still skipped
}

// SkipRun leaves one upcoming fire time out (at zero = the next run), or with undo puts
// it back. The schedule and its other runs are unchanged; next_run_at hops over skipped
// times. Skipping a time already skipped, or putting back one that isn't, is a no-op.
func (s *Service) SkipRun(ctx context.Context, taskID string, at time.Time, undo bool) (SkipResult, error) {
	if s.Sched == nil {
		return SkipResult{}, fmt.Errorf("scheduler unavailable")
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return SkipResult{}, err
	}
	defer tx.Rollback()
	var status, expr, raw string
	var next sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status, cron_expr, next_run_at, skip_runs FROM tasks WHERE id=?`, taskID).Scan(&status, &expr, &next, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SkipResult{}, fmt.Errorf("task not found")
		}
		return SkipResult{}, err
	}
	now := time.Now()
	skips := scheduler.FutureSkips(scheduler.ParseSkips(raw), now)
	if expr == "" || (status != "active" && !undo) {
		return SkipResult{}, ErrSkipNoSchedule
	}
	var nextAt time.Time
	if next.Valid {
		nextAt, _ = time.Parse(time.RFC3339, next.String)
	}
	if at.IsZero() {
		if undo {
			return SkipResult{}, fmt.Errorf("at required")
		}
		// The next run: next_run_at when it is still ahead, else (owed, held back by
		// "pause all") the first one ahead.
		if nextAt.After(now) {
			at = nextAt
		} else if at, err = s.Sched.NextSkipping(expr, now, skips); err != nil || at.IsZero() {
			return SkipResult{}, ErrSkipNotFireTime
		}
	}
	at = at.UTC().Truncate(time.Second)
	key := at.Format(time.RFC3339)
	has := false
	for _, x := range skips {
		if x == key {
			has = true
		}
	}
	switch {
	case !undo && !has:
		if !s.Sched.IsFireTime(expr, at, now, SkipHorizon) {
			return SkipResult{}, ErrSkipNotFireTime
		}
		if len(skips) >= scheduler.MaxSkips {
			return SkipResult{}, ErrSkipTooMany
		}
		skips = append(skips, key)
		if next.Valid && nextAt.Equal(at) {
			nx, err := s.Sched.NextSkipping(expr, at, skips)
			if err != nil {
				return SkipResult{}, err
			}
			nextAt = nx.UTC()
		}
	case undo && has:
		var keep []string
		for _, x := range skips {
			if x != key {
				keep = append(keep, x)
			}
		}
		skips = keep
		// Put back a time before the current next run (and still ahead): it is next again.
		if at.After(now) && (!next.Valid || at.Before(nextAt)) {
			nextAt = at
		}
	}
	nv := sql.NullString{}
	if !nextAt.IsZero() {
		nv = sql.NullString{String: nextAt.UTC().Format(time.RFC3339), Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET skip_runs=?, next_run_at=?, updated_at=? WHERE id=?`,
		scheduler.FormatSkips(skips), nv, nowUTC(), taskID); err != nil {
		return SkipResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SkipResult{}, err
	}
	left := scheduler.ParseSkips(scheduler.FormatSkips(skips))
	if left == nil {
		left = []string{}
	}
	return SkipResult{At: key, NextRunAt: nv.String, Skipped: left}, nil
}
