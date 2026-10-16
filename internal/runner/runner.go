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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

// Options configure a single Claude Code invocation.
type Options struct {
	ClaudeBin    string
	Prompt       string
	WorkspaceDir string
	Timeout      time.Duration
	Bare         bool
	ExtraArgs    []string
	Profile      provider.Profile
	APIKey       string // injected into child env only; never logged
	EnvBase      []string
	TriggerKind  string
	TaskID        string
	TaskVersionID string
	HasNextRunItems bool
	Logger        *slog.Logger
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
	Events       []Event
	ResultText   string
}

// Runner manages concurrency and persists runs to SQLite.
type Runner struct {
	store   *db.Store
	log     *slog.Logger
	sem     chan struct{}
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
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
		sem:     make(chan struct{}, maxConcurrency),
		cancels: map[string]context.CancelFunc{},
	}
}

// RunOnce executes a prompt under the concurrency limit and records the run.
func (r *Runner) RunOnce(ctx context.Context, opts Options) (Result, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
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

	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	r.mu.Lock()
	r.cancels[runID] = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.cancels, runID)
		r.mu.Unlock()
	}()

	started := time.Now().UTC()
	_ = r.store.MarkRunRunning(ctx, runID, started)

	log := opts.Logger
	if log == nil {
		log = r.log
	}
	log.Info("run started",
		"run_id", runID,
		"task_id", opts.TaskID,
		"provider", opts.Profile.Name,
		"model", opts.Profile.Model,
		"base_url", displayBase(opts.Profile.BaseURL),
		"claude_bin", opts.ClaudeBin,
	)

	res, err := execute(runCtx, opts)
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

// Cancel requests cancellation of an in-flight run.
func (r *Runner) Cancel(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cancels[runID]
	if ok {
		c()
	}
	return ok
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

func execute(ctx context.Context, opts Options) (Result, error) {
	bin := opts.ClaudeBin
	if bin == "" {
		bin = "claude"
	}
	args := []string{"-p", opts.Prompt, "--output-format", "stream-json", "--verbose"}
	if opts.Bare {
		args = append(args, "--bare")
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.CommandContext(ctx, bin, args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	baseEnv := opts.EnvBase
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	cmd.Env = provider.ChildEnv(baseEnv, opts.Profile, opts.APIKey)
	// Put process in its own group so we can signal the whole tree on cancel.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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

	var (
		events []Event
		result Result
		mu     sync.Mutex
		wg     sync.WaitGroup
	)
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
			mu.Lock()
			events = append(events, ev)
			if ev.IsResult() {
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

	waitErr := cmd.Wait()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	result.Events = events
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if ctx.Err() != nil {
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
