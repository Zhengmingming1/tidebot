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
	store  *db.Store
	log    *slog.Logger
	run    RunnerFunc
	tick   time.Duration
	parser cron.Parser
	// loc is the timezone cron fields are read in (nil = time.Local). It is applied to
	// every computation, so "0 9 * * *" means 9:00 there whatever zone the from time has.
	loc     atomic.Pointer[time.Location]
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	pmu     sync.Mutex
	paused  bool
	// pausedUntil is when a timed pause ends (zero = until resumed by hand).
	pausedUntil time.Time

	// OnAutoResume, when set, is called once (from the poll loop) after a timed pause ran
	// out and firing started again, so the owner can forget the stored pause.
	OnAutoResume func()

	// Wake, when set, is called at the start of every poll (also while firing is paused),
	// e.g. to resume tasks whose own timed pause has ended.
	Wake func(ctx context.Context, now time.Time)

	// Gate, when set, may defer a due task (e.g. on battery / about to sleep).
	// A deferred task is left due and retried on the next tick; its schedule is not advanced.
	Gate func(task db.Task) (deferRun bool, reason string)

	// Catchup, when set, returns how many of the missed fire times (oldest first) to run now
	// (final-plan §12: skip | run_once | run_all with a grace period). Nil means run once.
	Catchup func(task db.Task, missed []time.Time, now time.Time) int
}

// SetPaused pauses (until resumed by hand) or resumes firing of due tasks (manual runs
// are unaffected).
func (s *Scheduler) SetPaused(p bool) { s.PauseUntil(p, time.Time{}) }

// PauseUntil pauses firing until the given time (zero = until resumed by hand), or
// resumes when p is false. Tasks that come due meanwhile stay due; once firing starts
// again each follows its own missed-run policy (catch-up once / all / skip).
func (s *Scheduler) PauseUntil(p bool, until time.Time) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.paused = p
	s.pausedUntil = time.Time{}
	if p {
		s.pausedUntil = until
	}
}

// Paused reports whether the scheduler is paused right now (a timed pause that has run
// out counts as resumed even before the next poll clears it).
func (s *Scheduler) Paused() bool {
	p, _ := s.PauseState(time.Now())
	return p
}

// PausedUntil is when the current timed pause ends; zero when not paused or paused
// until resumed by hand.
func (s *Scheduler) PausedUntil() time.Time {
	_, u := s.PauseState(time.Now())
	return u
}

// PauseState reports, at now, whether firing is paused and until when (zero = by hand).
func (s *Scheduler) PauseState(now time.Time) (bool, time.Time) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	if !s.paused || (!s.pausedUntil.IsZero() && !now.Before(s.pausedUntil)) {
		return false, time.Time{}
	}
	return true, s.pausedUntil
}

// expirePause clears a timed pause whose end has passed; true when this call cleared it.
func (s *Scheduler) expirePause(now time.Time) bool {
	s.pmu.Lock()
	if !s.paused || s.pausedUntil.IsZero() || now.Before(s.pausedUntil) {
		s.pmu.Unlock()
		return false
	}
	s.paused, s.pausedUntil = false, time.Time{}
	s.pmu.Unlock()
	s.log.Info("scheduler pause ended, firing resumes")
	if s.OnAutoResume != nil {
		s.OnAutoResume()
	}
	return true
}

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
	now := time.Now()
	if s.Wake != nil {
		s.Wake(ctx, now)
	}
	s.expirePause(now)
	if p, _ := s.PauseState(now); p {
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
	if policy != "parallel" {
		active, err := s.store.HasActiveRun(ctx, task.ID)
		if err != nil {
			return err
		}
		if active {
			if policy == "queue" {
				// leave it due; it fires on the first tick after the active run ends
				s.log.Info("queue overlapping run", "task_id", task.ID)
				return nil
			}
			s.log.Info("skip overlapping run", "task_id", task.ID)
			return s.advanceSchedule(ctx, task, time.Now())
		}
	}

	now := time.Now().UTC()
	n := 1
	if s.Catchup != nil {
		n = s.Catchup(task, s.missedTimes(task, now), now)
		if n == 0 {
			s.log.Info("skip missed run(s)", "task_id", task.ID)
		}
	}
	if err := s.advanceSchedule(ctx, task, now); err != nil {
		return err
	}
	if s.run == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		if err := s.run(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

// missedTimes lists due fire times from next_run_at up to now (capped at 100).
func (s *Scheduler) missedTimes(task db.Task, now time.Time) []time.Time {
	if !task.NextRunAt.Valid {
		return []time.Time{now}
	}
	first, err := time.Parse(time.RFC3339, task.NextRunAt.String)
	if err != nil {
		return []time.Time{now}
	}
	out := []time.Time{first}
	t := first
	for len(out) < 100 {
		nx, err := s.NextRun(task.CronExpr, t)
		if err != nil || nx.After(now) {
			break
		}
		out = append(out, nx)
		t = nx
	}
	return out
}

func (s *Scheduler) advanceSchedule(ctx context.Context, task db.Task, from time.Time) error {
	next, err := s.NextRun(task.CronExpr, from)
	if err != nil {
		return err
	}
	return s.store.UpdateTaskSchedule(ctx, task.ID, &from, &next)
}

// SetLocation sets the timezone cron expressions are read in (nil = time.Local).
// Safe while running; call Realign afterwards so stored next runs follow.
func (s *Scheduler) SetLocation(loc *time.Location) { s.loc.Store(loc) }

// Location is the timezone cron expressions are read in.
func (s *Scheduler) Location() *time.Location {
	if l := s.loc.Load(); l != nil {
		return l
	}
	return time.Local
}

// NextRun computes the next fire time after from for a standard 5-field cron, read in
// the scheduler's timezone. robfig/cron reads the fields in from's own zone, so a from
// parsed from the database (UTC) used to move "0 9 * * *" to 9:00 UTC after the first run.
// Daylight saving changes follow the rules in dst.go (fixed times run once a day).
func (s *Scheduler) NextRun(expr string, from time.Time) (time.Time, error) {
	sched, err := s.parser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	nx := NextIn(sched, from, s.Location())
	if nx.IsZero() {
		return nx, nil
	}
	return nx.In(from.Location()), nil
}

// Realign recomputes next_run_at for scheduled tasks whose stored next run is still ahead
// but is not the cron's next fire time in the scheduler's timezone (written by an older
// version in the wrong zone, or the timezone was changed). Past-due times are left alone,
// so missed-run handling is unchanged. Returns how many tasks were corrected.
func (s *Scheduler) Realign(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.store.DB.QueryContext(ctx, `SELECT id, cron_expr, next_run_at FROM tasks
WHERE cron_expr <> '' AND next_run_at IS NOT NULL AND next_run_at > ?`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	type fix struct{ id, next string }
	var fixes []fix
	for rows.Next() {
		var id, expr, stored string
		if err := rows.Scan(&id, &expr, &stored); err != nil {
			rows.Close()
			return 0, err
		}
		nx, err := s.NextRun(expr, now)
		if err != nil || nx.IsZero() {
			continue // a broken expression is reported where it is edited
		}
		want := nx.UTC().Format(time.RFC3339)
		if st, err := time.Parse(time.RFC3339, stored); err == nil && st.UTC().Format(time.RFC3339) == want {
			continue
		}
		fixes = append(fixes, fix{id, want})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, f := range fixes {
		if _, err := s.store.DB.ExecContext(ctx, `UPDATE tasks SET next_run_at = ? WHERE id = ?`, f.next, f.id); err != nil {
			return 0, err
		}
		s.log.Info("realign next run", "task_id", f.id, "next_run_at", f.next, "timezone", s.Location().String())
	}
	return len(fixes), nil
}

// ComputeInitialNext returns the next run from now for a new task.
func (s *Scheduler) ComputeInitialNext(expr string) (time.Time, error) {
	return s.NextRun(expr, time.Now())
}
