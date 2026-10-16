// Package scheduler fires due cron tasks and enforces overlap policies.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/robfig/cron/v3"
)

// RunnerFunc executes a due task (injected to avoid import cycles).
type RunnerFunc func(ctx context.Context, task db.Task) error

// Scheduler polls SQLite for due tasks.
type Scheduler struct {
	store   *db.Store
	log     *slog.Logger
	run     RunnerFunc
	tick    time.Duration
	parser  cron.Parser
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	paused  atomic.Bool

	// Gate, when set, may defer a due task (e.g. on battery / about to sleep).
	// A deferred task is left due and retried on the next tick; its schedule is not advanced.
	Gate func(task db.Task) (deferRun bool, reason string)
}

// SetPaused pauses or resumes firing of due tasks (manual runs are unaffected).
func (s *Scheduler) SetPaused(p bool) { s.paused.Store(p) }

// Paused reports whether the scheduler is paused.
func (s *Scheduler) Paused() bool { return s.paused.Load() }

// New creates a scheduler. tick defaults to 15s.
func New(store *db.Store, log *slog.Logger, tick time.Duration, run RunnerFunc) *Scheduler {
	if tick <= 0 {
		tick = 15 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		store:  store,
		log:    log,
		run:    run,
		tick:   tick,
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor),
	}
}

// Start begins the poll loop until Stop or ctx cancel.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.running = true
	s.mu.Unlock()

	go func() {
		t := time.NewTicker(s.tick)
		defer t.Stop()
		s.poll(ctx)
		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
				return
			case <-t.C:
				s.poll(ctx)
			}
		}
	}()
}

// Stop cancels the poll loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Scheduler) poll(ctx context.Context) {
	if s.paused.Load() {
		return
	}
	due, err := s.store.ListDueTasks(ctx, time.Now())
	if err != nil {
		s.log.Error("list due tasks", "error", err)
		return
	}
	for _, task := range due {
		if s.Gate != nil {
			if d, why := s.Gate(task); d {
				s.log.Info("defer due task", "task_id", task.ID, "reason", why)
				continue
			}
		}
		if err := s.handleDue(ctx, task); err != nil {
			s.log.Error("handle due task", "task_id", task.ID, "error", err)
		}
	}
}

func (s *Scheduler) handleDue(ctx context.Context, task db.Task) error {
	policy := task.OverlapPolicy
	if policy == "" {
		policy = "skip"
	}
	if policy == "skip" {
		active, err := s.store.HasActiveRun(ctx, task.ID)
		if err != nil {
			return err
		}
		if active {
			s.log.Info("skip overlapping run", "task_id", task.ID)
			// still advance next_run_at so we don't spin
			return s.advanceSchedule(ctx, task, time.Now())
		}
	}

	fired := time.Now().UTC()
	if err := s.advanceSchedule(ctx, task, fired); err != nil {
		return err
	}
	if s.run == nil {
		return nil
	}
	return s.run(ctx, task)
}

func (s *Scheduler) advanceSchedule(ctx context.Context, task db.Task, from time.Time) error {
	next, err := s.NextRun(task.CronExpr, from)
	if err != nil {
		return err
	}
	return s.store.UpdateTaskSchedule(ctx, task.ID, &from, &next)
}

// NextRun computes the next fire time after from for a standard 5-field cron.
func (s *Scheduler) NextRun(expr string, from time.Time) (time.Time, error) {
	sched, err := s.parser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}

// ComputeInitialNext returns the next run from now for a new task.
func (s *Scheduler) ComputeInitialNext(expr string) (time.Time, error) {
	return s.NextRun(expr, time.Now())
}
