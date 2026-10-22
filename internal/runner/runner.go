// Package runner spawns Claude Code (or a mock), parses stream-json, and records usage.
package runner

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/pricing"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

// Options configure a single Claude Code invocation.
type Options struct {
	ClaudeBin       string
	Prompt          string
	WorkspaceDir    string
	Timeout         time.Duration
	Bare            bool
	ExtraArgs       []string
	Profile         provider.Profile
	APIKey          string // injected into child env only; never logged
	EnvBase         []string
	TriggerKind     string
	TaskID          string
	TaskVersionID   string
	HasNextRunItems bool
	CostBucket      string // production|canary|optimization|dry_run|assist
	Logger          *slog.Logger
	// RunID is filled in by RunOnce (used to label the live process list).
	RunID string
	// OnStart, if set, receives the run id as soon as the run row exists (live progress).
	OnStart func(runID string)
	// NoPluginMCP keeps the owner's plugin connectors out of this run (AI plugin builder).
	NoPluginMCP bool
	// Cost prices the run from token usage and enforces caps mid-run (nil = no pricing).
	Cost *CostControl
	// OnUsage, if set, receives the run's usage so far each time the runner saves it
	// mid-run (see Runner.UsageSaveEvery), e.g. to keep the spend ledger current.
	// Not called for the final cost: that is the returned Result.Cost.
	OnUsage func(runID string, c pricing.Cost)
	// BeforeStart, if set, runs once a run that had to wait in line gets its slot,
	// just before Claude Code is launched. Things checked when the run was queued
	// (budgets, the task still being active) may have changed during the wait: it
	// can tighten the options (e.g. a smaller cost cap) or return an error to not
	// start at all. Such a run ends as skipped (see ErrSkipped) and frees the slot.
	BeforeStart func(ctx context.Context, o *Options) error
}

// Result is the outcome of one run.
type Result struct {
	RunID        string
	Status       string // succeeded|failed|canceled|timeout
	SessionID    string
	ExitCode     int
	ErrorMessage string
	TotalCostUSD float64
	InputTokens  int
	OutputTokens int
	DurationMS   int64
	ResultText   string
	// Cost is tidebot's own pricing of the run (zero when Options.Cost is nil).
	Cost pricing.Cost
}

// ErrShuttingDown is returned for runs refused or stopped because the core is
// quitting (Drain). Callers treat it as "not a failure": no retry, no breaker
// strike, no "run failed" notice.
var ErrShuttingDown = errors.New("tidebot is shutting down")

// Runner manages concurrency and persists runs to SQLite.
type Runner struct {
	store   *db.Store
	log     *slog.Logger
	slots   slots
	mu      sync.Mutex
	cancels map[string]context.CancelCauseFunc
	// Drain state: once closing is set, RunOnce refuses new work and queued runs
	// stop waiting for a slot. active counts RunOnce calls in flight; idle is
	// closed when it drops to zero while a Drain is waiting.
	closing  bool
	stopping bool // grace period over: runs registered from now on are canceled at once
	quit     chan struct{}
	active   int
	idle     chan struct{}
	bells    logBells
	// LogDir, when set, receives a human-readable log per run (<LogDir>/<run_id>.log)
	// that the WebUI tails for live SSE output. Secrets are masked before writing.
	LogDir string
	// UsageSaveEvery is how often a running run's usage is saved (0 = default 5s,
	// < 0 = only at the end). Saving mid-run means a crash keeps the spend.
	UsageSaveEvery time.Duration
}

// LogPath returns the log file path for a run ("" if logging disabled).
func (r *Runner) LogPath(runID string) string {
	if r.LogDir == "" {
		return ""
	}
	return filepath.Join(r.LogDir, runID+".log")
}

// New creates a Runner with a concurrency limit (default 2).
func New(store *db.Store, log *slog.Logger, maxConcurrency int) *Runner {
	if maxConcurrency < 1 {
		maxConcurrency = 2
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		store:   store,
		log:     log,
		slots:   slots{max: maxConcurrency},
		cancels: map[string]context.CancelCauseFunc{},
		quit:    make(chan struct{}),
	}
}

// enter registers a RunOnce call; false once Drain has started.
func (r *Runner) enter() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.active++
	return true
}

func (r *Runner) leave() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.active == 0 && r.idle != nil {
		close(r.idle)
		r.idle = nil
	}
}

// Active reports how many runs are queued or running in this process.
func (r *Runner) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// DrainResult says how a Drain went.
type DrainResult struct {
	Active   int      // runs in flight when Drain started
	Stopped  []string // run ids canceled after the grace period
	TimedOut bool     // some run still hadn't written its final state at the end
}

// Drain prepares the runner for exit. It refuses new runs, releases runs still
// waiting for a concurrency slot, gives running ones up to grace to finish on
// their own, then cancels the rest (status canceled, partial token usage kept)
// and waits up to writeWait for them to record that. It never blocks longer
// than grace+writeWait, so a stuck child can't hold up quitting. Safe to call
// more than once.
func (r *Runner) Drain(grace, writeWait time.Duration) DrainResult {
	r.mu.Lock()
	if !r.closing {
		r.closing = true
		close(r.quit)
	}
	res := DrainResult{Active: r.active}
	if r.active == 0 {
		r.mu.Unlock()
		return res
	}
	if r.idle == nil {
		r.idle = make(chan struct{})
	}
	idle := r.idle
	r.mu.Unlock()

	if waitClosed(idle, grace) {
		return res
	}
	r.mu.Lock()
	r.stopping = true
	for runID, c := range r.cancels {
		c(ErrShuttingDown)
		res.Stopped = append(res.Stopped, runID)
	}
	r.mu.Unlock()
	res.TimedOut = !waitClosed(idle, writeWait)
	return res
}

func waitClosed(ch <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

// RunOnce executes a prompt under the concurrency limit and records the run.
// The run row is written first (status queued), so a run waiting for a slot is
// visible, counts as active for its task's overlap policy, and can be stopped
// (Cancel) before it starts. Its timeout only starts once it has a slot.
func (r *Runner) RunOnce(ctx context.Context, opts Options) (Result, error) {
	if !r.enter() {
		return Result{}, ErrShuttingDown
	}
	defer r.leave()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	runID := id.New("run")
	created := time.Now().UTC().Format(time.RFC3339)
	row := db.Run{
		ID:           runID,
		Status:       "queued",
		TriggerKind:  nullDefault(opts.TriggerKind, "manual"),
		Prompt:       opts.Prompt,
		WorkspaceDir: opts.WorkspaceDir,
		CreatedAt:    created,
		ProviderName: sql.NullString{String: opts.Profile.Name, Valid: opts.Profile.Name != ""},
		Model:        sql.NullString{String: opts.Profile.Model, Valid: opts.Profile.Model != ""},
	}
	if opts.TaskID != "" {
		row.TaskID = sql.NullString{String: opts.TaskID, Valid: true}
	}
	if opts.TaskVersionID != "" {
		row.TaskVersionID = sql.NullString{String: opts.TaskVersionID, Valid: true}
	}
	row.HasNextRunItems = opts.HasNextRunItems
	if err := r.store.InsertRun(ctx, row); err != nil {
		return Result{}, fmt.Errorf("insert run: %w", err)
	}
	if opts.CostBucket != "" {
		_, _ = r.store.DB.ExecContext(ctx, `UPDATE runs SET cost_bucket=? WHERE id=?`, opts.CostBucket, runID)
	}

	// The cause tells execute why it was canceled (user, or Drain on quit).
	// Registered before waiting for a slot, so a queued run can be stopped too.
	runCtx, cancel := context.WithCancelCause(ctx)
	r.mu.Lock()
	r.cancels[runID] = cancel
	if r.stopping { // Drain already canceled the others
		cancel(ErrShuttingDown)
	}
	r.mu.Unlock()
	defer func() {
		cancel(nil)
		r.mu.Lock()
		delete(r.cancels, runID)
		r.mu.Unlock()
	}()

	log := opts.Logger
	if log == nil {
		log = r.log
	}
	var logW io.Writer = io.Discard
	if lp := r.LogPath(runID); lp != "" {
		if err := os.MkdirAll(r.LogDir, 0o700); err == nil {
			if f, ferr := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); ferr == nil {
				defer f.Close()
				defer r.bells.release(runID)
				logW = r.LogWriter(runID, f)
			}
		}
	}
	if opts.OnStart != nil {
		opts.OnStart(runID)
	}

	waited := false
	err := r.slots.acquire(runCtx, r.quit, runID, func(ahead int) {
		waited = true
		fmt.Fprintf(logW, "[%s] queued: waiting for a free slot (%d ahead)\n", time.Now().Format("15:04:05"), ahead)
		log.Info("run queued", "run_id", runID, "task_id", opts.TaskID, "ahead", ahead)
	})
	if err == nil {
		select { // a slot and quitting may have come together; quitting wins
		case <-r.quit:
			r.slots.release()
			err = errQuit
		default:
		}
	}
	if err != nil {
		return r.finishUnstarted(runID, opts, logW, err)
	}
	defer r.slots.release()

	if waited && opts.BeforeStart != nil {
		if why := opts.BeforeStart(runCtx, &opts); why != nil {
			return r.finishSkipped(runID, opts, logW, why)
		}
	}

	if opts.Timeout > 0 {
		var stop context.CancelFunc
		runCtx, stop = context.WithTimeout(runCtx, opts.Timeout)
		defer stop()
	}

	started := time.Now().UTC()
	_ = r.store.MarkRunRunning(ctx, runID, started)

	log.Info("run started",
		"run_id", runID,
		"task_id", opts.TaskID,
		"provider", opts.Profile.Name,
		"model", opts.Profile.Model,
		"base_url", displayBase(opts.Profile.BaseURL),
		"claude_bin", opts.ClaudeBin,
	)

	fmt.Fprintf(logW, "[%s] run %s started (provider=%s)\n", started.Local().Format("15:04:05"), runID, opts.Profile.Name)
	if opts.RunID == "" {
		opts.RunID = runID
	}
	var onUsage func(pricing.Cost)
	if every := r.usageEvery(); every > 0 && opts.Cost != nil {
		onUsage = func(c pricing.Cost) {
			// Own short deadline: the run's context may be canceled, and a busy
			// database must not hold the saver (stop waits for it).
			wctx, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer done()
			if err := r.store.UpdateRunCost(wctx, runID, c); err != nil {
				log.Warn("save run usage", "error", err, "run_id", runID)
				return
			}
			if opts.OnUsage != nil {
				opts.OnUsage(runID, c)
			}
		}
	}
	res, err := execute(runCtx, opts, logW, r.usageEvery(), onUsage)
	res.RunID = runID
	finished := time.Now().UTC()
	res.DurationMS = finished.Sub(started).Milliseconds()

	status := res.Status
	if status == "" {
		if err != nil {
			status = "failed"
			res.ErrorMessage = err.Error()
		} else {
			status = "succeeded"
		}
	}
	res.Status = status

	finish := db.Run{
		ID:           runID,
		Status:       status,
		SessionID:    nullStr(res.SessionID),
		ExitCode:     sql.NullInt64{Int64: int64(res.ExitCode), Valid: true},
		ErrorMessage: nullStr(res.ErrorMessage),
		TotalCostUSD: sql.NullFloat64{Float64: res.TotalCostUSD, Valid: res.TotalCostUSD > 0 || status == "succeeded"},
		InputTokens:  sql.NullInt64{Int64: int64(res.InputTokens), Valid: true},
		OutputTokens: sql.NullInt64{Int64: int64(res.OutputTokens), Valid: true},
		DurationMS:   sql.NullInt64{Int64: res.DurationMS, Valid: true},
		StartedAt:    sql.NullString{String: started.Format(time.RFC3339), Valid: true},
		FinishedAt:   sql.NullString{String: finished.Format(time.RFC3339), Valid: true},
		ProviderName: sql.NullString{String: opts.Profile.Name, Valid: opts.Profile.Name != ""},
		Model:        sql.NullString{String: opts.Profile.Model, Valid: opts.Profile.Model != ""},
	}
	if opts.TaskID != "" {
		finish.TaskID = sql.NullString{String: opts.TaskID, Valid: true}
	}
	if dbErr := r.store.UpdateRunFinish(context.Background(), finish); dbErr != nil {
		log.Error("persist run finish failed", "error", dbErr, "run_id", runID)
	}
	if opts.Cost != nil {
		if dbErr := r.store.UpdateRunCost(context.Background(), runID, res.Cost); dbErr != nil {
			log.Error("persist run cost failed", "error", dbErr, "run_id", runID)
		}
	}

	if opts.Cost != nil {
		fmt.Fprintf(logW, "[%s] run finished: %s (cost %.4f %s [%s], %d ms)\n[[end]]\n", time.Now().Format("15:04:05"), status, res.Cost.Amount, res.Cost.Currency, res.Cost.Source, res.DurationMS)
	} else {
		fmt.Fprintf(logW, "[%s] run finished: %s (cost $%.4f, %d ms)\n[[end]]\n", time.Now().Format("15:04:05"), status, res.TotalCostUSD, res.DurationMS)
	}
	log.Info("run finished",
		"run_id", runID,
		"status", status,
		"exit_code", res.ExitCode,
		"duration_ms", res.DurationMS,
		"cost_usd", res.TotalCostUSD,
		"session_id", res.SessionID,
	)
	return res, err
}

func (r *Runner) usageEvery() time.Duration {
	if r.UsageSaveEvery == 0 {
		return DefaultUsageSaveEvery
	}
	return r.UsageSaveEvery
}

// Cancel stops a run of this process on the user's behalf: a queued one leaves
// the line and never starts, a running one is killed (usage so far is kept).
// Either ends as canceled with db.RunStoppedByUserMsg. False if the run isn't
// queued or running here.
func (r *Runner) Cancel(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cancels[runID]
	if ok {
		c(ErrStoppedByUser)
	}
	return ok
}

// finishUnstarted records a run that gave up waiting for a slot (stopped by the
// user, quitting, or the caller's context) as canceled. It never started, so it
// has no start time, no duration and no cost.
func (r *Runner) finishUnstarted(runID string, opts Options, logW io.Writer, why error) (Result, error) {
	res := Result{RunID: runID, Status: "canceled"}
	var err error
	switch {
	case errors.Is(why, errQuit), errors.Is(why, ErrShuttingDown):
		res.ErrorMessage, err = db.RunStoppedOnQuitMsg, ErrShuttingDown
	case errors.Is(why, ErrStoppedByUser):
		res.ErrorMessage, err = db.RunStoppedByUserMsg, ErrStoppedByUser
	default:
		res.ErrorMessage, err = "run canceled", why
	}
	fin := db.Run{
		ID: runID, Status: res.Status, ErrorMessage: nullStr(res.ErrorMessage),
		FinishedAt:   sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true},
		ProviderName: sql.NullString{String: opts.Profile.Name, Valid: opts.Profile.Name != ""},
		Model:        sql.NullString{String: opts.Profile.Model, Valid: opts.Profile.Model != ""},
	}
	if dbErr := r.store.UpdateRunFinish(context.Background(), fin); dbErr != nil {
		r.log.Error("persist run finish failed", "error", dbErr, "run_id", runID)
	}
	fmt.Fprintf(logW, "[%s] run finished: canceled before it started (%s)\n[[end]]\n", time.Now().Format("15:04:05"), res.ErrorMessage)
	return res, err
}

// ErrSkipped wraps the reason a queued run was not started when it got its slot
// (Options.BeforeStart said no). The run row ends as skipped, with no start time
// and no cost.
var ErrSkipped = errors.New("skipped")

// finishSkipped records a queued run that BeforeStart turned away. The stored
// message is "skipped: <why>" so the WebUI can explain it in the viewer's language.
func (r *Runner) finishSkipped(runID string, opts Options, logW io.Writer, why error) (Result, error) {
	msg := "skipped: " + why.Error()
	res := Result{RunID: runID, Status: "skipped", ErrorMessage: msg}
	fin := db.Run{
		ID: runID, Status: res.Status, ErrorMessage: nullStr(msg),
		FinishedAt:   sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true},
		ProviderName: sql.NullString{String: opts.Profile.Name, Valid: opts.Profile.Name != ""},
		Model:        sql.NullString{String: opts.Profile.Model, Valid: opts.Profile.Model != ""},
	}
	if opts.TaskID != "" {
		fin.TaskID = sql.NullString{String: opts.TaskID, Valid: true}
	}
	if dbErr := r.store.UpdateRunFinish(context.Background(), fin); dbErr != nil {
		r.log.Error("persist run finish failed", "error", dbErr, "run_id", runID)
	}
	fmt.Fprintf(logW, "[%s] run finished: skipped before it started (%s)\n[[end]]\n", time.Now().Format("15:04:05"), why)
	r.log.Info("run skipped at start", "run_id", runID, "task_id", opts.TaskID, "why", why.Error())
	return res, fmt.Errorf("%w: %w", ErrSkipped, why)
}

func displayBase(u string) string {
	if u == "" {
		return "(default)"
	}
	return u
}

func nullDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func execute(ctx context.Context, opts Options, logW io.Writer, usageEvery time.Duration, onUsage func(pricing.Cost)) (Result, error) {
	if logW == nil {
		logW = io.Discard
	}
	bin := opts.ClaudeBin
	if bin == "" {
		bin = "claude"
	}
	args := []string{"-p", opts.Prompt, "--output-format", "stream-json", "--verbose"}
	if opts.Bare {
		args = append(args, "--bare")
	}
	args = append(args, opts.ExtraArgs...)

	// budgetStop kills the run when CostControl says the caps are exceeded.
	ctx, budgetStop := context.WithCancel(ctx)
	defer budgetStop()
	var meter *pricing.Meter
	if opts.Cost != nil {
		meter = pricing.NewMeter(opts.Cost.Calc)
	}
	budgetMsg := ""
	var resultEv *Event

	cmd := exec.CommandContext(ctx, bin, args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	baseEnv := opts.EnvBase
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	cmd.Env = withProxyEnv(provider.ChildEnv(baseEnv, opts.Profile, opts.APIKey))
	// Own process group (unix) / job object (windows) so the whole tree dies on cancel.
	setupProcess(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Status: "failed", ErrorMessage: err.Error()}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{Status: "failed", ErrorMessage: err.Error()}, err
	}

	if err := cmd.Start(); err != nil {
		return Result{Status: "failed", ErrorMessage: err.Error(), ExitCode: -1}, err
	}
	release := afterStart(cmd)
	defer release()
	untrack := trackProc(cmd.Process.Pid, opts.RunID)
	defer untrack()

	var (
		result Result
		mu     sync.Mutex
		wg     sync.WaitGroup
		saver  *usageSaver
	)
	if meter != nil && onUsage != nil && usageEvery > 0 {
		var saved pricing.Usage
		saver = startUsageSaver(usageEvery, func() (pricing.Cost, bool) {
			mu.Lock()
			defer mu.Unlock()
			c := meter.Now()
			if !usageChanged(c.Totals, saved) {
				return c, false
			}
			saved = c.Totals
			return c, true
		}, onUsage)
	}
	// Stopped before the caller's final write, so a mid-run save never lands after it.
	defer saver.stop()
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = drainMasked(stderr, opts.APIKey)
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		// stream-json lines can be large
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 10*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(bytesTrimSpace(line)) == 0 {
				continue
			}
			ev, err := ParseEvent(line)
			if err != nil {
				continue // skip malformed lines; mock/tests still pass on valid ones
			}
			if s := FormatEvent(ev); s != "" {
				fmt.Fprintf(logW, "[%s] %s\n", time.Now().Format("15:04:05"), provider.MaskSecret(s, opts.APIKey))
			}
			// Events are not kept: a long run streams megabytes of tool output,
			// and everything needed afterwards is read here as it arrives.
			mu.Lock()
			if meter != nil && budgetMsg == "" {
				if mid, u, ok := assistantUsage(ev); ok {
					meter.Add(mid, u)
					saver.changed()
					if msg := opts.Cost.exceeded(meter.Now()); msg != "" {
						budgetMsg = msg
						fmt.Fprintf(logW, "[%s] %s — stopping run\n", time.Now().Format("15:04:05"), msg)
						budgetStop()
					}
				}
			}
			if ev.IsResult() {
				evc := ev
				resultEv = &evc
				result.SessionID = ev.SessionID
				result.TotalCostUSD = ev.TotalCostUSD
				result.ResultText = ev.Result
				result.DurationMS = ev.DurationMS
				if ev.Usage != nil {
					result.InputTokens = ev.Usage.InputTokens
					result.OutputTokens = ev.Usage.OutputTokens
				}
				if ev.IsError {
					result.Status = "failed"
					result.ErrorMessage = ev.Result
				}
			}
			if ev.SessionID != "" && result.SessionID == "" {
				result.SessionID = ev.SessionID
			}
			mu.Unlock()
		}
	}()

	// Read everything before Wait: Wait closes the pipes and would drop the tail
	// (including the final result event) on a fast-exiting process.
	wg.Wait()
	waitErr := cmd.Wait()

	saver.stop()
	mu.Lock()
	defer mu.Unlock()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if meter != nil {
		var totals map[string]pricing.Usage
		if resultEv != nil {
			totals = resultTotals(*resultEv, opts.Profile.Model)
		}
		result.Cost = opts.Cost.Calc.Compute(meter.Requests(), totals, result.TotalCostUSD)
	}
	if budgetMsg != "" {
		result.Status = "canceled"
		result.ErrorMessage = budgetMsg
		return result, errors.New(budgetMsg)
	}

	if ctx.Err() != nil {
		if errors.Is(context.Cause(ctx), ErrShuttingDown) {
			fmt.Fprintf(logW, "[%s] tidebot is quitting — run stopped\n", time.Now().Format("15:04:05"))
			result.Status = "canceled"
			result.ErrorMessage = db.RunStoppedOnQuitMsg
			return result, ErrShuttingDown
		}
		if errors.Is(context.Cause(ctx), ErrStoppedByUser) {
			fmt.Fprintf(logW, "[%s] stopped by user\n", time.Now().Format("15:04:05"))
			result.Status = "canceled"
			result.ErrorMessage = db.RunStoppedByUserMsg
			return result, ErrStoppedByUser
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Status = "timeout"
			result.ErrorMessage = "run timed out"
			return result, ctx.Err()
		}
		result.Status = "canceled"
		result.ErrorMessage = "run canceled"
		return result, ctx.Err()
	}
	if waitErr != nil {
		if result.Status == "" {
			result.Status = "failed"
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = waitErr.Error()
		}
		return result, waitErr
	}
	if result.Status == "" {
		result.Status = "succeeded"
	}
	return result, nil
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func drainMasked(r io.Reader, secret string) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		_ = provider.MaskSecret(sc.Text(), secret) // discard; available for future log hooks
	}
	return sc.Err()
}

// withProxyEnv adds tidebot's resolved proxy (Settings → 网络 or the OS proxy)
// to Claude Code's environment, unless the variables are already set there.
func withProxyEnv(env []string) []string {
	have := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		have[k] = true
	}
	for _, kv := range netproxy.Env() {
		k, _, _ := strings.Cut(kv, "=")
		if !have[k] {
			env = append(env, kv)
		}
	}
	return env
}
