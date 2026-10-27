// Package policy holds per-task failure, retry, catch-up and priority policies (final-plan §12).
package policy

import (
	"context"
	"database/sql"
	"math/rand"
	"strings"
	"time"
)

// Policy is the per-task configuration stored in task_policies.
type Policy struct {
	TaskID              string `json:"task_id"`
	Catchup             string `json:"catchup"` // skip | run_once | run_all
	CatchupGraceMinutes int    `json:"catchup_grace_minutes"`
	RetryMax            int    `json:"retry_max"`
	Priority            string `json:"priority"` // urgent | normal | background
	AllowedTools        string `json:"allowed_tools"`
	SideEffects         bool   `json:"side_effects"`
	NotifySuccess       bool   `json:"notify_success"`
	// NotifyAnswer: the success notice carries the first lines of the final answer.
	NotifyAnswer        bool `json:"notify_answer"`
	ConsecutiveFailures int  `json:"consecutive_failures"`
}

// Defaults (suggested values from the plan).
const (
	BreakerThreshold = 3
	MaxCatchupRuns   = 5
)

// Default returns the default policy for a task.
func Default(taskID string) Policy {
	return Policy{TaskID: taskID, Catchup: "run_once", CatchupGraceMinutes: 360, RetryMax: 2, Priority: "normal", NotifyAnswer: true}
}

// Store persists policies.
type Store struct{ DB *sql.DB }

// Get returns the stored policy or the default.
func (s *Store) Get(ctx context.Context, taskID string) Policy {
	p := Default(taskID)
	var se, ns, na int
	err := s.DB.QueryRowContext(ctx, `SELECT catchup, catchup_grace_minutes, retry_max, priority, allowed_tools, side_effects, notify_success, notify_answer, consecutive_failures
FROM task_policies WHERE task_id=?`, taskID).Scan(&p.Catchup, &p.CatchupGraceMinutes, &p.RetryMax, &p.Priority, &p.AllowedTools, &se, &ns, &na, &p.ConsecutiveFailures)
	if err != nil {
		return Default(taskID)
	}
	p.SideEffects, p.NotifySuccess, p.NotifyAnswer = se == 1, ns == 1, na == 1
	return p
}

// Validate normalizes and checks a policy.
func Validate(p *Policy) error {
	switch p.Catchup {
	case "skip", "run_once", "run_all":
	case "":
		p.Catchup = "run_once"
	default:
		return errInvalid("catchup")
	}
	switch p.Priority {
	case "urgent", "normal", "background":
	case "":
		p.Priority = "normal"
	default:
		return errInvalid("priority")
	}
	if p.RetryMax < 0 || p.RetryMax > 5 {
		return errInvalid("retry_max (0..5)")
	}
	if p.CatchupGraceMinutes <= 0 {
		p.CatchupGraceMinutes = 360
	}
	return nil
}

type errInvalid string

func (e errInvalid) Error() string { return "invalid policy field: " + string(e) }

// Put upserts a policy (consecutive_failures is preserved).
func (s *Store) Put(ctx context.Context, p Policy) error {
	if err := Validate(&p); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO task_policies(task_id, catchup, catchup_grace_minutes, retry_max, priority, allowed_tools, side_effects, notify_success, notify_answer, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(task_id) DO UPDATE SET catchup=excluded.catchup, catchup_grace_minutes=excluded.catchup_grace_minutes,
 retry_max=excluded.retry_max, priority=excluded.priority, allowed_tools=excluded.allowed_tools,
 side_effects=excluded.side_effects, notify_success=excluded.notify_success, notify_answer=excluded.notify_answer, updated_at=excluded.updated_at`,
		p.TaskID, p.Catchup, p.CatchupGraceMinutes, p.RetryMax, p.Priority, p.AllowedTools, b2i(p.SideEffects), b2i(p.NotifySuccess), b2i(p.NotifyAnswer), now())
	return err
}

// RecordOutcome updates the consecutive-failure counter and reports whether the breaker tripped.
func (s *Store) RecordOutcome(ctx context.Context, taskID string, ok bool) (failures int, tripped bool, err error) {
	p := s.Get(ctx, taskID)
	if ok {
		p.ConsecutiveFailures = 0
	} else {
		p.ConsecutiveFailures++
	}
	if err := s.Put(ctx, p); err != nil {
		return 0, false, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE task_policies SET consecutive_failures=? WHERE task_id=?`, p.ConsecutiveFailures, taskID)
	return p.ConsecutiveFailures, !ok && p.ConsecutiveFailures >= BreakerThreshold, err
}

// Classify maps a run error to a class and whether a run-level retry makes sense.
func Classify(msg string) (class string, retryable bool) {
	m := strings.ToLower(msg)
	switch {
	case m == "":
		return "ok", false
	case strings.Contains(m, "budget"):
		return "budget", false
	case strings.Contains(m, "authentication") || strings.Contains(m, "401") || strings.Contains(m, "api key"):
		return "authentication_failed", false
	case strings.Contains(m, "billing"):
		return "billing_error", false
	case strings.Contains(m, "invalid_request") || strings.Contains(m, "invalid request"):
		return "invalid_request", false
	case strings.Contains(m, "blocked by guard") || strings.Contains(m, "dangerous"):
		return "danger_blocked", false
	case strings.Contains(m, "acceptance"):
		return "acceptance_failed", false
	case strings.Contains(m, "rate_limit") || strings.Contains(m, "rate limit") || strings.Contains(m, "429"):
		return "rate_limit", true
	case strings.Contains(m, "overloaded") || strings.Contains(m, "529"):
		return "overloaded", true
	case strings.Contains(m, "server_error") || strings.Contains(m, "500") || strings.Contains(m, "502") || strings.Contains(m, "503"):
		return "server_error", true
	case strings.Contains(m, "connection") || strings.Contains(m, "network") || strings.Contains(m, "eof") || strings.Contains(m, "timeout"):
		return "network", true
	case strings.Contains(m, "exit status") || strings.Contains(m, "signal"):
		return "process_exit", true
	}
	return "unknown", false
}

// Backoff returns the wait before retry attempt n (1-based): 1m, 5m, then 5m, ±20% jitter.
func Backoff(n int, r *rand.Rand) time.Duration {
	base := time.Minute
	if n >= 2 {
		base = 5 * time.Minute
	}
	j := 0.8 + 0.4*r.Float64()
	return time.Duration(float64(base) * j)
}

// CatchupRuns decides how many missed fire times to run now.
// missed are the fire times strictly before now that were not run, oldest first.
func CatchupRuns(policy string, missed []time.Time, now time.Time, grace time.Duration) int {
	var inGrace int
	for _, t := range missed {
		if now.Sub(t) <= grace {
			inGrace++
		}
	}
	if inGrace == 0 {
		return 0
	}
	switch policy {
	case "skip":
		// The most recent slot counts as "on time" if it is within one minute.
		if now.Sub(missed[len(missed)-1]) <= time.Minute {
			return 1
		}
		return 0
	case "run_all":
		if inGrace > MaxCatchupRuns {
			return MaxCatchupRuns
		}
		return inGrace
	default:
		return 1
	}
}

// AllowedOnBattery reports whether a task of this priority may start on battery / low power.
func AllowedOnBattery(priority string) bool { return priority == "urgent" }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
