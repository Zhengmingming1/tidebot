// Package app wires config, database, providers, runner and scheduler for agentd/agentctl.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/notify"
	"github.com/Zhengmingming1/tidebot/internal/optimize"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/policy"

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

	trig *triggerState

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
	a.Notify.Loc = cfg.Location()
	a.Sched = scheduler.New(store, log, cfg.SchedulerTick, a.runTaskScheduled)
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
	a.Cost.Loc = cfg.Location()
	a.Auth = auth.New(sqlDB)
	if err := a.Cost.EnsureDefaults(context.Background()); err != nil {
		log.Warn("cost defaults", "error", err)
	}
	if err := a.Auth.EnsureOwner(context.Background()); err != nil {
		log.Warn("auth owner", "error", err)
	}
	a.applyProviderSettings(context.Background())
	// Restore webui.enabled from settings if present.
	if v, err := a.Auth.GetSetting(context.Background(), "webui.enabled"); err == nil && v != "" {
		a.Cfg.WebUIEnabled = v == "true" || v == "1"
	}
	a.WebUI = webui.New(webui.Deps{
		Cfg: &a.Cfg, DB: sqlDB, Store: store, Auth: a.Auth, Cost: a.Cost,
		Tasks: a.Tasks, Amend: a.Amend, Skills: a.Skills, Ver: ver, Reg: reg, Log: log,
		RunTask: a.startTaskAsync, QuickRun: a.startQuickRunAsync,
		LogPath: a.Runner.LogPath, Location: cfg.Location(),
		Ext:           a.CallExt,
		QuickRunSkill: a.startQuickRunSkill,
		Providers:     providerAdmin{a},
		Public:        map[string]http.Handler{"/hooks/": a.WebhookHandler(), "/metrics": a.MetricsHandler()},
	})
	return a, nil
}

// Close releases DB resources.
func (a *App) Close() error {
	if a.Sched != nil {
		a.Sched.Stop()
	}
	if a.WebUI != nil {
		_ = a.WebUI.Stop(context.Background())
	}
	if a.DB != nil {
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
	opts.ClaudeBin = a.Cfg.ClaudeBin
	opts.Timeout = timeout
	opts.Bare = true
	opts.Profile = prof
	opts.APIKey = key
	opts.Logger = a.Log
	bucket := opts.CostBucket
	if bucket == "" {
		bucket = cost.BucketProduction
	}
	runMax := 0.05
	if a.Cost != nil {
		chk, err := a.Cost.CheckBeforeRun(ctx, opts.TaskID, bucket, runMax)
		if err != nil {
			return runner.Result{}, err
		}
		if !chk.OK {
			_ = a.Cost.LogBreach(ctx, chk.BreachScope, opts.TaskID, bucket, "cap", chk.Reason, chk.BreachAction)
			a.Events.Emit(Event{Kind: EventBudgetBreached, TaskID: opts.TaskID, TaskName: a.taskName(opts.TaskID), Detail: chk.Reason})
			if opts.TaskID != "" && (chk.BreachAction == "pause_notify" || chk.BreachAction == "") {
				_ = a.Cost.PauseTask(ctx, opts.TaskID, chk.Reason)
			}
			return runner.Result{}, fmt.Errorf("budget blocked: %s", chk.Reason)
		}
		if chk.Warn {
			a.Log.Warn("budget warning", "detail", chk.WarnDetail)
		}
		runMax = chk.RunMaxUSD
		if runMax <= 0 {
			runMax = 0.01
		}
		if bucket == cost.BucketDryRun && runMax > DryRunBudgetUSD {
			runMax = DryRunBudgetUSD
		}
	}
	if len(opts.ExtraArgs) == 0 {
		opts.ExtraArgs = []string{
			"--permission-mode", "dontAsk",
			"--permission-prompts", "none",
			"--restricted",
			"--max-budget-usd", fmt.Sprintf("%.4f", runMax),
			"--effort", "low",
		}
	}
	opts.ExtraArgs = append(opts.ExtraArgs, a.mcpArgs(ctx, bucket)...)
	if err := a.applyGuard(ctx, &opts); err != nil {
		return runner.Result{}, err
	}
	// Keep Claude Code from self-updating mid-run; version is recorded per run.
	opts.EnvBase = append(opts.EnvBase, "DISABLE_AUTOUPDATER=1")
	res, err := a.Runner.RunOnce(ctx, opts)
	a.recordRunMeta(ctx, res.RunID)
	if err == nil && a.Cost != nil {
		_ = a.Cost.Record(ctx, res.RunID, opts.TaskID, bucket, res.TotalCostUSD, res.InputTokens, res.OutputTokens)
	}
	ev := Event{Kind: EventRunFinished, TaskID: opts.TaskID, TaskName: a.taskName(opts.TaskID), RunID: res.RunID, CostUSD: res.TotalCostUSD}
	if err != nil {
		ev.Kind, ev.Detail = EventRunFailed, err.Error()
	} else if res.Status != "" && res.Status != "succeeded" {
		ev.Kind, ev.Detail = EventRunFailed, res.Status+": "+res.ErrorMessage
	}
	a.Events.Emit(ev)
	return res, err
}

func isMockBin(bin string) bool {
	return bin != "" && (strings.Contains(bin, "mock-claude") || bin == "true")
}

func (a *App) runTask(ctx context.Context, task db.Task) error {
	return a.runTaskTrigger(ctx, task, "cron", cost.BucketProduction)
}

func (a *App) runTaskTrigger(ctx context.Context, task db.Task, trigger, bucket string) error {
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
	if view.Spec != nil && len(view.Spec.SkillBindings) > 0 {
		var bindings []tasksvc.SkillBinding
		bindings, choices = a.chooseSkillVersions(ctx, task.ID, bucket, view.Spec.SkillBindings)
		for _, c := range choices {
			if c.Candidate {
				bucket = cost.BucketCanary
			}
		}
		if err := a.Skills.Materialize(ctx, ws, bindings); err != nil {
			a.Log.Warn("materialize skills", "error", err)
		}
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

// startTaskAsync runs a task in the background (WebUI buttons / Run now).
func (a *App) startTaskAsync(taskID, bucket string) error {
	task, err := a.Store.GetTask(context.Background(), taskID)
	if err != nil {
		return fmt.Errorf("task not found")
	}
	go func() {
		if err := a.runTaskTrigger(context.Background(), *task, "manual", bucket); err != nil {
			a.Log.Warn("manual task run", "task_id", taskID, "error", err)
		}
	}()
	return nil
}

// startQuickRunAsync runs an ad-hoc prompt in the background.
func (a *App) startQuickRunAsync(prompt, providerName string) error {
	if _, err := a.ResolveProfile(providerName); err != nil {
		return err
	}
	go func() {
		_, err := a.RunPromptEx(context.Background(), runner.Options{
			Prompt: prompt, TriggerKind: "quick", CostBucket: cost.BucketProduction,
		}, providerName, 0)
		if err != nil {
			a.Log.Warn("quick run", "error", err)
		}
	}()
	return nil
}

// startQuickRunSkill runs an ad-hoc prompt in a scratch workspace with one skill materialized.
func (a *App) startQuickRunSkill(prompt, providerName, skillID, versionID string) error {
	if _, err := a.ResolveProfile(providerName); err != nil {
		return err
	}
	mode := "follow"
	if versionID != "" {
		mode = "pinned"
	}
	ws, err := os.MkdirTemp(a.Cfg.DataDir, "button-")
	if err != nil {
		return err
	}
	if err := a.Skills.Materialize(context.Background(), ws, []tasksvc.SkillBinding{{SkillID: skillID, Mode: mode, VersionID: versionID}}); err != nil {
		os.RemoveAll(ws)
		return fmt.Errorf("skill: %w", err)
	}
	go func() {
		defer os.RemoveAll(ws)
		_, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: prompt, WorkspaceDir: ws, TriggerKind: "quick", CostBucket: cost.BucketProduction}, providerName, 0)
		if err != nil {
			a.Log.Warn("button run", "error", err)
		}
	}()
	return nil
}
