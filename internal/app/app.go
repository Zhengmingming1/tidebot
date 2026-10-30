// Package app wires config, database, providers, runner and scheduler for agentd/agentctl.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/notify"
	"github.com/Zhengmingming1/tidebot/internal/optimize"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/pricing"

	"github.com/Zhengmingming1/tidebot/internal/amend"
	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/logging"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/version"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// App is the core runtime shared by daemon and CLI.
type App struct {
	Cfg        config.Config
	Log        *slog.Logger
	DB         *sql.DB
	suOnce     sync.Once
	fxCache    atomic.Pointer[pricing.FX]
	suState    *selfUpdate
	Store      *db.Store
	Registry   *provider.Registry
	Secrets    secrets.Backend
	Runner     *runner.Runner
	Sched      *scheduler.Scheduler
	Ver        *version.Service
	Tasks      *tasksvc.Service
	Amend      *amend.Service
	Skills     *skillstore.Service
	Cost       *cost.Service
	Auth       *auth.Service
	WebUI      *webui.Server
	Events     *Bus
	Policy     *policy.Store
	Notify     *notify.Service
	Optimize   *optimize.Service
	Plugins    *plugin.Store
	PluginHost *plugin.Host

	trig  *triggerState
	disk  diskWatch
	power powerWatch
	loc   atomic.Pointer[time.Location] // scheduling timezone (see timezone.go)

	retryScale float64
	// HookExe is the binary that implements `hook pretool` (defaults to os.Executable()).
	HookExe string
}

// Open loads config, opens DB, and builds subsystems.
func Open(cfg config.Config) (*App, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	log := logging.New(logging.Options{Level: cfg.LogLevel, Format: cfg.LogFormat})
	sqlDB, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	store := db.NewStore(sqlDB)

	fileProfiles, err := provider.LoadFile(cfg.ProvidersFile)
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	reg, err := provider.NewRegistry(fileProfiles)
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	ver := version.New(sqlDB)
	a := &App{
		Cfg:      cfg,
		Log:      log,
		DB:       sqlDB,
		Store:    store,
		Registry: reg,
		Secrets:  secrets.Chain{secrets.NewFileBackend(cfg.HomeDir), secrets.EnvBackend{}},
		Runner:   runner.New(store, log, cfg.MaxConcurrency),
		Ver:      ver,
	}
	a.Runner.LogDir = filepath.Join(cfg.DataDir, "runs")
	a.Policy = &policy.Store{DB: sqlDB}
	a.Notify = notify.New(sqlDB, a.Secrets)
	a.Notify.LocFn = a.Loc
	a.Sched = scheduler.New(store, log, cfg.SchedulerTick, a.runTaskScheduled)
	a.Sched.SetLocation(cfg.Location()) // until the stored zone is read below
	a.Sched.Catchup = a.catchup
	if v, err := strconv.ParseFloat(os.Getenv("TIDEBOT_RETRY_SCALE"), 64); err == nil && v > 0 {
		a.retryScale = v
	}
	a.Tasks = tasksvc.New(store, ver, a.Sched)
	structurer := amend.Structurer(amend.MockStructurer{})
	if os.Getenv("TIDEBOT_AMEND_AI") == "1" {
		structurer = &ClaudeStructurer{App: a}
	}
	a.Amend = amend.New(sqlDB, a.Tasks, ver, structurer)
	a.Events = &Bus{}
	a.wireChannelHealth()
	a.Amend.OnProposed = func(taskID string, items []amend.Item) {
		a.Events.Emit(Event{Kind: EventApprovalNeeded, TaskID: taskID, TaskName: a.taskName(taskID), Count: len(items)})
	}
	a.Skills = skillstore.New(sqlDB, ver, a.Tasks, "")
	a.Optimize = optimize.New(sqlDB, a.Skills, ver)
	a.Optimize.OnEvent = func(kind, skillID, detail string) {
		a.Events.Emit(Event{Kind: kind, Detail: detail, TaskName: a.skillName(skillID)})
	}
	a.initPlugins()
	a.Cost = cost.New(sqlDB, log)
	a.Cost.LocFn = a.Loc
	a.Cost.FX = a.cachedFX
	a.Auth = auth.New(sqlDB)
	if mk, err := secrets.NewFileBackend(cfg.HomeDir).MasterKey(); err != nil {
		log.Error("2fa key unavailable", "error", err)
	} else if err := a.Auth.SetMasterKey(mk); err != nil {
		log.Error("2fa key", "error", err)
	}
	if err := a.Cost.EnsureDefaults(context.Background()); err != nil {
		log.Warn("cost defaults", "error", err)
	}
	if err := a.Auth.EnsureOwner(context.Background()); err != nil {
		log.Warn("auth owner", "error", err)
	}
	if n, err := a.Auth.MigrateTOTP(context.Background()); err != nil {
		log.Warn("2fa secret migration", "error", err)
	} else if n > 0 {
		log.Info("2fa secret encrypted at rest", "rows", n)
	}
	a.applyLoc(a.resolveLoc(context.Background()))
	a.applyProviderSettings(context.Background())
	a.Sched.OnAutoResume = a.onSchedPauseEnded
	a.Sched.Wake = a.wakeSnoozedTasks
	a.loadSchedPause(context.Background())
	// Restore webui.enabled from settings if present.
	if v, err := a.Auth.GetSetting(context.Background(), "webui.enabled"); err == nil && v != "" {
		a.Cfg.WebUIEnabled = v == "true" || v == "1"
	}
	a.WebUI = webui.New(webui.Deps{
		Cfg: &a.Cfg, DB: sqlDB, Store: store, Auth: a.Auth, Cost: a.Cost,
		Tasks: a.Tasks, Amend: a.Amend, Skills: a.Skills, Ver: ver, Reg: reg, Log: log,
		RunTask: a.startTaskAsync, QuickRun: a.startQuickRunAsync,
		StopRun:  func(id string) (string, error) { return a.StopRun(context.Background(), id) },
		QueuePos: a.Runner.QueuePos,
		LogPath:  a.Runner.LogPath, LogWait: a.Runner.LogWait, Location: cfg.Location(), LocFn: a.Loc,
		Ext: a.CallExt,
		ClaudeStatus: func(ctx context.Context, force bool) any {
			st := a.ClaudeStatus(ctx, force)
			sp := a.SchedPauseBrief(ctx)
			st.Sched = &sp
			return st
		},
		SchedPause:    func(ctx context.Context) any { return a.SchedPauseBrief(ctx) },
		QuickRunSkill: a.startQuickRunSkill, StartRun: a.startRun,
		Providers: providerAdmin{a},
		Public:    map[string]http.Handler{"/hooks/": a.WebhookHandler(), "/metrics": a.MetricsHandler()},
	})
	return a, nil
}

// Close releases DB resources.
func (a *App) Close() error {
	if a.Sched != nil {
		a.Sched.Stop()
	}
	a.drainRuns()
	if a.WebUI != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.WebUI.Stop(sctx)
		cancel()
	}
	if a.DB != nil {
		octx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = db.Optimize(octx, a.DB)
		cancel()
		return a.DB.Close()
	}
	return nil
}

// ResolveAPIKey loads the key for a profile (env / secret backend). Never log the return value.
func (a *App) ResolveAPIKey(p provider.Profile) (string, error) {
	if key, err := a.Secrets.Get("provider:" + p.Name); err == nil && key != "" {
		return key, nil
	}
	return provider.EnvFromOS(p)
}

// ResolveProfile picks a provider by name (or default).
func (a *App) ResolveProfile(name string) (provider.Profile, error) {
	if name == "" {
		name = a.Cfg.DefaultProvider
	}
	p, ok := a.Registry.Get(name)
	if !ok {
		return provider.Profile{}, fmt.Errorf("unknown provider %q", name)
	}
	return p, nil
}

// RunPrompt executes an ad-hoc or task-bound prompt once.
func (a *App) RunPrompt(ctx context.Context, prompt, workspace, providerName, taskID, trigger string, timeout time.Duration) (runner.Result, error) {
	return a.RunPromptEx(ctx, runner.Options{
		Prompt: prompt, WorkspaceDir: workspace, TriggerKind: trigger, TaskID: taskID,
	}, providerName, timeout)
}

// RunPromptEx is the full run entry used by task execution.
func (a *App) RunPromptEx(ctx context.Context, opts runner.Options, providerName string, timeout time.Duration) (runner.Result, error) {
	prof, err := a.ResolveProfile(providerName)
	if err != nil {
		return runner.Result{}, err
	}
	key, err := a.ResolveAPIKey(prof)
	if err != nil {
		if !isMockBin(a.Cfg.ClaudeBin) {
			return runner.Result{}, fmt.Errorf("resolve api key for %s: %w", prof.Name, err)
		}
		key = ""
	}
	if timeout <= 0 {
		timeout = a.Cfg.DefaultTimeout
	}
	if opts.WorkspaceDir == "" {
		opts.WorkspaceDir = a.Cfg.DataDir
	}
	bin, env := a.claudeExec(ctx)
	opts.ClaudeBin = bin
	if opts.EnvBase == nil {
		opts.EnvBase = env
	}
	opts.Timeout = timeout
	opts.Bare = true
	opts.Profile = prof
	opts.APIKey = key
	opts.Logger = a.Log
	bucket := opts.CostBucket
	if bucket == "" {
		bucket = cost.BucketProduction
	}
	callerCap := callerCapUSD(opts.ExtraArgs)
	runMax, cc, err := a.admitRun(ctx, opts.TaskID, bucket, prof, callerCap)
	if err != nil {
		return runner.Result{}, err
	}
	opts.Cost = cc
	if len(opts.ExtraArgs) == 0 {
		opts.ExtraArgs = []string{
			"--permission-mode", "dontAsk",
			"--permission-prompts", "none",
			"--restricted",
			"--max-budget-usd", "0",
			"--effort", "low",
		}
	}
	opts.ExtraArgs = a.budgetArgs(opts.ExtraArgs, prof, runMax)
	if !opts.NoPluginMCP {
		opts.ExtraArgs = append(opts.ExtraArgs, a.mcpArgs(ctx, bucket)...)
	}
	if err := a.applyGuard(ctx, &opts); err != nil {
		return runner.Result{}, err
	}
	// Keep Claude Code from self-updating mid-run; version is recorded per run.
	opts.EnvBase = append(opts.EnvBase, "DISABLE_AUTOUPDATER=1")
	// Its own state for the run stays in tidebot's directory, not the owner's home.
	// Only for --bare runs: they authenticate strictly with the provider's key
	// (Claude Code never reads its subscription login or the keychain there), so
	// moving the config dir can't sign anything out. A run that used the owner's
	// `claude login` must not get CLAUDE_CONFIG_DIR: the login lives under the
	// config dir (.credentials.json) or in a keychain item named after it (macOS).
	if opts.Bare {
		opts.EnvBase = append(opts.EnvBase, a.isolationEnv()...)
	}
	if a.modernClaude() {
		opts.ContextArgs = append(opts.ContextArgs, "--no-session-persistence")
	}
	// The run's cap is held against the daily budgets from its start until it ends
	// (see cost.ReserveRun), so runs going at the same time share what is left.
	var hold atomic.Pointer[cost.Hold]
	defer func() { hold.Load().Release() }()
	if a.Cost != nil {
		// Keep the ledger current while the run goes, so budgets and the spend page
		// see a long run's cost before it ends, and a crash doesn't lose it.
		opts.OnUsage = func(runID string, c pricing.Cost) {
			wctx, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer done()
			_ = a.Cost.RecordEntry(wctx, cost.EntryFromCost(runID, opts.TaskID, bucket, prof.Name, prof.Model, c))
			hold.Load().SetSpent(c.Amount, c.Currency)
		}
		opts.AtStart = func(sctx context.Context, o *runner.Options) error {
			h, err := a.holdRunBudget(sctx, o, bucket, prof)
			hold.Store(h)
			return err
		}
	}
	// A run that waited for a slot is checked again before it starts: runs ahead
	// of it may have used up the day's budget, or the task was paused meanwhile.
	taskID, trigger := opts.TaskID, opts.TriggerKind
	opts.StartCheck = func(sctx context.Context) error {
		if err := a.diskStartCheck(sctx); err != nil {
			return err
		}
		return a.powerStartCheck(sctx, taskID, trigger) // scheduled runs of non-urgent tasks on battery
	}
	opts.BeforeStart = func(sctx context.Context, o *runner.Options) error {
		if !a.taskStillRunnable(sctx, o.TaskID, o.TriggerKind) {
			return errTaskInactive
		}
		rm, ncc, err := a.admitRun(sctx, o.TaskID, bucket, prof, callerCap)
		if err != nil {
			return err
		}
		o.Cost = ncc
		o.ExtraArgs = a.budgetArgs(o.ExtraArgs, prof, rm)
		return nil
	}
	res, err := a.Runner.RunOnce(ctx, opts)
	a.recordRunMeta(ctx, res.RunID)
	// failed / stopped runs still spent tokens: record them too. The caller's
	// context may already be canceled (quit, user stop), so don't write under it.
	if res.RunID != "" && a.Cost != nil {
		wctx, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		_ = a.Cost.RecordEntry(wctx, cost.EntryFromCost(res.RunID, opts.TaskID, bucket, prof.Name, prof.Model, res.Cost))
		done()
	}
	if errors.Is(err, runner.ErrShuttingDown) || errors.Is(err, runner.ErrStoppedByUser) || errors.Is(err, runner.ErrSkipped) {
		// The user stopped it, or is quitting, or it was skipped at its slot (a budget
		// notice was already sent): the run row says so, no "failed" notice.
		return res, err
	}
	ev := Event{Kind: EventRunFinished, TaskID: opts.TaskID, TaskName: a.taskName(opts.TaskID), RunID: res.RunID,
		CostUSD: a.RunCostUSD(res)}
	if err != nil {
		ev.Kind, ev.Detail = EventRunFailed, err.Error()
	} else if res.Status != "" && res.Status != "succeeded" {
		ev.Kind, ev.Detail = EventRunFailed, res.Status+": "+res.ErrorMessage
	} else {
		a.markAnswerSame(ctx, &ev)
		a.attachAnswer(ctx, &ev)
	}
	a.Events.Emit(ev)
	return res, err
}

// errTaskInactive: a scheduled run's task was paused, archived or deleted while
// the run waited for a slot. Not the task's failure: no retry, no breaker strike.
var errTaskInactive = errors.New("task paused")

// taskStillRunnable reports whether a queued run may still start. Scheduled runs
// (cron, retry) of a task that is no longer active don't; ad-hoc runs and runs
// the user started by hand always may (Run now works on a paused task too).
func (a *App) taskStillRunnable(ctx context.Context, taskID, trigger string) bool {
	if taskID == "" || (trigger != "cron" && trigger != "retry") {
		return true
	}
	t, err := a.Store.GetTask(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return err != nil || t.Status == "active" // a read error must not drop the run
}

// admitRun checks the budgets (global, bucket, task) and the provider quota, and
// returns the per-run cap (display currency) with the cost control for the run.
// A breach is logged, notified and (per the budget's action) pauses the task.
// It runs when a run is asked for and again when a queued run gets its slot.
func (a *App) admitRun(ctx context.Context, taskID, bucket string, prof provider.Profile, callerCap float64) (float64, *runner.CostControl, error) {
	runMax := 0.05
	if a.Cost != nil {
		chk, err := a.Cost.CheckBeforeRun(ctx, taskID, bucket, runMax)
		if err != nil {
			return 0, nil, err
		}
		if !chk.OK {
			return 0, nil, a.budgetBreach(ctx, taskID, bucket, chk)
		}
		if chk.Warn {
			a.Log.Warn("budget warning", "detail", chk.WarnDetail)
			a.warnOnce(chk.WarnDetail)
		}
		runMax = chk.RunMaxUSD // display currency
		f := a.cachedFX()
		if runMax <= 0 {
			runMax = f.Convert(0.01, pricing.USD, f.Display)
		}
		if dr := f.Convert(DryRunBudgetUSD, pricing.USD, f.Display); bucket == cost.BucketDryRun && runMax > dr {
			runMax = dr
		}
	} else {
		f := a.cachedFX()
		runMax = f.Convert(runMax, pricing.USD, f.Display)
	}
	if c := callerCap; c > 0 {
		f := a.cachedFX()
		if v := f.Convert(c, pricing.USD, f.Display); v < runMax {
			runMax = v
		}
	}
	cc, err := a.costControl(ctx, prof, runMax)
	if err != nil {
		if a.Cost != nil {
			_ = a.Cost.LogBreach(ctx, "provider", prof.Name, bucket, "quota", err.Error(), "skip")
		}
		a.Events.Emit(Event{Kind: EventBudgetBreached, TaskID: taskID, TaskName: a.taskName(taskID), Detail: err.Error()})
		return 0, nil, fmt.Errorf("budget blocked: %w", err)
	}
	return runMax, cc, nil
}

// errBudgetHeld: what is left of a daily budget is held by runs already going.
// Not the task's fault: the run is skipped, no retry, no breaker strike, no pause.
var errBudgetHeld = errors.New("budget blocked")

// budgetBreach logs and notifies a refused run and (per the budget's action)
// pauses the task. A Held refusal is only logged: the task stays on.
func (a *App) budgetBreach(ctx context.Context, taskID, bucket string, chk cost.CheckResult) error {
	if chk.Held {
		_ = a.Cost.LogBreach(ctx, chk.BreachScope, taskID, bucket, "held", chk.Reason, chk.BreachAction)
		a.Log.Info("run skipped: budget held by running runs", "task_id", taskID, "detail", chk.Reason)
		return fmt.Errorf("%w: %s", errBudgetHeld, chk.Reason)
	}
	_ = a.Cost.LogBreach(ctx, chk.BreachScope, taskID, bucket, "cap", chk.Reason, chk.BreachAction)
	a.Events.Emit(Event{Kind: EventBudgetBreached, TaskID: taskID, TaskName: a.taskName(taskID), Detail: chk.Reason})
	if taskID != "" && (chk.BreachAction == "pause_notify" || chk.BreachAction == "") {
		_ = a.Cost.PauseTask(ctx, taskID, chk.Reason)
	}
	return fmt.Errorf("budget blocked: %s", chk.Reason)
}

// holdRunBudget runs right before a run launches: it holds the run's money cap
// against the daily budgets and lowers it to what runs already going left over.
// Runs without a money cap (subscriptions) hold nothing.
func (a *App) holdRunBudget(ctx context.Context, o *runner.Options, bucket string, prof provider.Profile) (*cost.Hold, error) {
	if o.Cost == nil || o.Cost.MaxAmount <= 0 {
		return nil, nil
	}
	h, capAmt, chk, err := a.Cost.ReserveRun(ctx, o.TaskID, bucket, o.Cost.MaxAmount, o.Cost.Currency)
	if err != nil {
		a.Log.Warn("budget hold", "error", err) // a read error must not drop the run
		return nil, nil
	}
	if !chk.OK {
		return nil, a.budgetBreach(ctx, o.TaskID, bucket, chk)
	}
	if capAmt < o.Cost.MaxAmount {
		cc := *o.Cost
		cc.MaxAmount = capAmt
		o.Cost = &cc
		o.ExtraArgs = a.budgetArgs(o.ExtraArgs, prof, a.cachedFX().Convert(capAmt, cc.Currency, a.cachedFX().Display))
	}
	return h, nil
}

func isMockBin(bin string) bool {
	return bin != "" && (strings.Contains(bin, "mock-claude") || bin == "true")
}

func (a *App) runTaskTrigger(ctx context.Context, task db.Task, trigger, bucket string) error {
	return a.runTaskTriggerOn(ctx, task, trigger, bucket, nil)
}

// runTaskTriggerOn is runTaskTrigger with an OnStart hook that receives the run id.
func (a *App) runTaskTriggerOn(ctx context.Context, task db.Task, trigger, bucket string, onStart func(string)) error {
	providerName := a.Cfg.DefaultProvider
	view, err := a.Tasks.Get(ctx, task.ID)
	if err == nil && view.Spec != nil && view.Spec.Provider != "" {
		providerName = view.Spec.Provider
	} else if task.ProviderID.Valid && task.ProviderID.String != "" {
		if prow, err := a.Store.GetProviderByID(ctx, task.ProviderID.String); err == nil {
			providerName = prow.Name
		}
	}
	prompt, versionID, nextIDs, err := a.Tasks.CompilePrompt(ctx, task.ID)
	if err != nil {
		return err
	}
	if prompt == "" {
		prompt = task.Prompt
	}
	prompt = externalPrompt(ctx, prompt)
	ws := task.WorkspaceDir
	if view.Spec != nil && view.Spec.WorkspaceDir != "" {
		ws = view.Spec.WorkspaceDir
	}
	if ws == "" {
		ws = guard.TaskWorkspace(a.Cfg.HomeDir, task.ID)
		_ = os.MkdirAll(ws, 0o755)
	}
	var choices []skillChoice
	var skillArgs []string
	if view.Spec != nil && len(view.Spec.SkillBindings) > 0 {
		var bindings []tasksvc.SkillBinding
		bindings, choices = a.chooseSkillVersions(ctx, task.ID, bucket, view.Spec.SkillBindings)
		for _, c := range choices {
			if c.Candidate {
				bucket = cost.BucketCanary
			}
		}
		// Staged in tidebot's own directory, never in ws (see skillstage.go).
		st, err := a.stageSkills(ctx, bindings)
		if err != nil {
			a.Log.Warn("stage skills", "task_id", task.ID, "error", err)
		}
		defer st.Close()
		skillArgs = st.Args(a.modernClaude(), false)
	}
	timeout := time.Duration(task.TimeoutMinutes) * time.Minute
	if view.Spec != nil && view.Spec.TimeoutMinutes > 0 {
		timeout = time.Duration(view.Spec.TimeoutMinutes) * time.Minute
	}
	if timeout <= 0 {
		timeout = a.Cfg.DefaultTimeout
	}
	res, err := a.RunPromptEx(ctx, runner.Options{
		Prompt: prompt, WorkspaceDir: ws, TriggerKind: trigger, TaskID: task.ID,
		TaskVersionID: versionID, HasNextRunItems: len(nextIDs) > 0, CostBucket: bucket,
		OnStart: onStart, ContextArgs: skillArgs,
	}, providerName, timeout)
	if err == nil && len(nextIDs) > 0 {
		_ = a.Amend.MarkConsumed(ctx, res.RunID, nextIDs)
	}
	a.recordSkillOutcomes(ctx, choices, res, err, len(nextIDs) > 0)
	if err != nil && res.ErrorMessage != "" && !strings.Contains(err.Error(), res.ErrorMessage) {
		err = fmt.Errorf("%s: %w", res.ErrorMessage, err)
	}
	if err == nil && res.Status != "" && res.Status != "succeeded" {
		err = fmt.Errorf("run %s: %s", res.Status, res.ErrorMessage)
	}
	return err
}

// EnsureTask creates a versioned task (Phase 1) with optional cron.
func (a *App) EnsureTask(ctx context.Context, name, prompt, cronExpr, providerName, workspace string) (db.Task, error) {
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{
		Name: name, Prompt: prompt, WorkspaceDir: workspace, ProviderName: providerName,
		CronExpr: cronExpr, OverlapPolicy: "skip",
		TimeoutMinutes: int(a.Cfg.DefaultTimeout / time.Minute), Actor: "user",
	})
	if err != nil {
		return db.Task{}, err
	}
	return tv.Task, nil
}

// SeedBuiltinProviders writes built-in presets into SQLite (idempotent by name).
func (a *App) SeedBuiltinProviders(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range a.Registry.List() {
		if _, err := a.Store.GetProviderByName(ctx, p.Name); err == nil {
			continue
		}
		row := db.ProviderRow{
			ID: id.New("prov"), Name: p.Name,
			Preset:  sql.NullString{String: p.Preset, Valid: p.Preset != ""},
			BaseURL: p.BaseURL, AuthEnv: p.AuthEnv, Model: p.Model,
			OpusModel: p.OpusModel, SonnetModel: p.SonnetModel, HaikuModel: p.HaikuModel,
			FableModel: p.FableModel, SmallFastModel: p.SmallFastModel, SubagentModel: p.SubagentModel,
			ExtraEnvJSON: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := a.Store.UpsertProvider(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// StopRun stops a run on the user's behalf (WebUI Stop, agentctl runs stop): a queued
// run leaves the line and never starts, a running one is killed. It reports the
// status the run had ("queued" or "running"); an error means it isn't active here.
func (a *App) StopRun(ctx context.Context, runID string) (string, error) {
	var st string
	if err := a.DB.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, runID).Scan(&st); err != nil {
		return "", ErrRunNotFound
	}
	if (st != "queued" && st != "running") || !a.Runner.Cancel(runID) {
		return st, ErrRunNotActive
	}
	return st, nil
}

// ErrRunNotFound / ErrRunNotActive are StopRun's answers for unknown and finished runs.
var (
	ErrRunNotFound  = errors.New("run not found")
	ErrRunNotActive = errors.New("run is not queued or running")
)

// startWait is how long a WebUI start waits for the new run's row, so it can answer with
// the run id. The row is written before the run waits for a slot, so queued runs answer
// with their id too; only a start held up before that (e.g. a busy database) has none.
const startWait = 3 * time.Second

// launch runs fn in the background and waits until the run row exists (its id is returned),
// fn fails before creating a row (that error is returned, e.g. budget blocked), or wait
// passes (empty id, the run keeps going). fn's final error is passed to onErr either way.
func launch(wait time.Duration, fn func(onStart func(string)) error, onErr func(error)) (string, error) {
	started := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		err := fn(func(id string) {
			select {
			case started <- id:
			default:
			}
		})
		if err != nil && onErr != nil {
			onErr(err)
		}
		done <- err
	}()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case id := <-started:
		return id, nil
	case err := <-done:
		select {
		case id := <-started: // ran (and maybe failed) quickly: the run page shows why
			return id, nil
		default:
		}
		return "", err
	case <-t.C:
		return "", nil
	}
}

// startTaskAsync runs a task in the background (WebUI buttons / Run now) and returns the
// new run's id once it exists ("" if it is still queued).
func (a *App) startTaskAsync(taskID, bucket string) (string, error) {
	task, err := a.Store.GetTask(context.Background(), taskID)
	if err != nil {
		return "", fmt.Errorf("task not found")
	}
	return launch(startWait, func(onStart func(string)) error {
		return a.runTaskTriggerOn(context.Background(), *task, "manual", bucket, onStart)
	}, func(err error) { a.Log.Warn("manual task run", "task_id", taskID, "error", err) })
}

// startQuickRunAsync runs an ad-hoc prompt in the background and returns the run id.
func (a *App) startQuickRunAsync(prompt, providerName string) (string, error) {
	if _, err := a.ResolveProfile(providerName); err != nil {
		return "", err
	}
	return launch(startWait, func(onStart func(string)) error {
		_, err := a.RunPromptEx(context.Background(), runner.Options{
			Prompt: prompt, TriggerKind: "quick", CostBucket: cost.BucketProduction, OnStart: onStart,
		}, providerName, 0)
		return err
	}, func(err error) { a.Log.Warn("quick run", "error", err) })
}

// startQuickRunSkill runs an ad-hoc prompt with one skill (buttons). The skill is
// staged in tidebot's own directory and invoked as its slash command; the run's
// working folder is an empty scratch folder inside the same stage.
func (a *App) startQuickRunSkill(prompt, providerName, skillID, versionID string) (string, error) {
	if _, err := a.ResolveProfile(providerName); err != nil {
		return "", err
	}
	mode := "follow"
	if versionID != "" {
		mode = "pinned"
	}
	st, err := a.stageSkills(context.Background(), []tasksvc.SkillBinding{{SkillID: skillID, Mode: mode, VersionID: versionID}})
	if err != nil {
		return "", fmt.Errorf("skill: %w", err)
	}
	ws := filepath.Join(st.Dir, "work")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		st.Close()
		return "", err
	}
	modern := a.modernClaude()
	return launch(startWait, func(onStart func(string)) error {
		defer st.Close()
		_, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: st.Invoke(modern, prompt), WorkspaceDir: ws, TriggerKind: "quick",
			CostBucket: cost.BucketProduction, OnStart: onStart, ContextArgs: st.Args(modern, true)}, providerName, 0)
		return err
	}, func(err error) { a.Log.Warn("button run", "error", err) })
}
