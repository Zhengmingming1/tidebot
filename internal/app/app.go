// Package app wires config, database, providers, runner and scheduler for agentd/agentctl.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/logging"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/scheduler"
	"github.com/Zhengmingming1/tidebot/internal/secrets"
)

// App is the core runtime shared by daemon and CLI.
type App struct {
	Cfg      config.Config
	Log      *slog.Logger
	DB       *sql.DB
	Store    *db.Store
	Registry *provider.Registry
	Secrets  secrets.Backend
	Runner   *runner.Runner
	Sched    *scheduler.Scheduler
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

	a := &App{
		Cfg:      cfg,
		Log:      log,
		DB:       sqlDB,
		Store:    store,
		Registry: reg,
		Secrets:  secrets.Chain{secrets.EnvBackend{}},
		Runner:   runner.New(store, log, cfg.MaxConcurrency),
	}
	a.Sched = scheduler.New(store, log, cfg.SchedulerTick, a.runTask)
	return a, nil
}

// Close releases DB resources.
func (a *App) Close() error {
	if a.Sched != nil {
		a.Sched.Stop()
	}
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// ResolveAPIKey loads the key for a profile (env / secret backend). Never log the return value.
func (a *App) ResolveAPIKey(p provider.Profile) (string, error) {
	// Prefer dedicated secret name, then provider.EnvFromOS fallbacks.
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
	prof, err := a.ResolveProfile(providerName)
	if err != nil {
		return runner.Result{}, err
	}
	key, err := a.ResolveAPIKey(prof)
	if err != nil {
		// Allow mock runs without a key when Claude bin is the mock.
		if !isMockBin(a.Cfg.ClaudeBin) {
			return runner.Result{}, fmt.Errorf("resolve api key for %s: %w", prof.Name, err)
		}
		key = ""
	}
	if timeout <= 0 {
		timeout = a.Cfg.DefaultTimeout
	}
	if workspace == "" {
		workspace = a.Cfg.DataDir
	}
	return a.Runner.RunOnce(ctx, runner.Options{
		ClaudeBin:    a.Cfg.ClaudeBin,
		Prompt:       prompt,
		WorkspaceDir: workspace,
		Timeout:      timeout,
		Bare:         true,
		Profile:      prof,
		APIKey:       key,
		TriggerKind:  trigger,
		TaskID:       taskID,
		Logger:       a.Log,
	})
}

func isMockBin(bin string) bool {
	return bin != "" && (strings.Contains(bin, "mock-claude") || bin == "true")
}

func (a *App) runTask(ctx context.Context, task db.Task) error {
	providerName := a.Cfg.DefaultProvider
	if task.ProviderID.Valid && task.ProviderID.String != "" {
		if prow, err := a.Store.GetProviderByID(ctx, task.ProviderID.String); err == nil {
			providerName = prow.Name
		}
	}
	timeout := time.Duration(task.TimeoutMinutes) * time.Minute
	if timeout <= 0 {
		timeout = a.Cfg.DefaultTimeout
	}
	_, err := a.RunPrompt(ctx, task.Prompt, task.WorkspaceDir, providerName, task.ID, "cron", timeout)
	return err
}

// EnsureTask creates a simple Phase-0 task with cron (overlap=skip).
func (a *App) EnsureTask(ctx context.Context, name, prompt, cronExpr, providerName, workspace string) (db.Task, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var providerID sql.NullString
	if providerName != "" {
		if p, err := a.Store.GetProviderByName(ctx, providerName); err == nil {
			providerID = sql.NullString{String: p.ID, Valid: true}
		} else {
			// seed from registry into DB
			prof, err := a.ResolveProfile(providerName)
			if err != nil {
				return db.Task{}, err
			}
			row := db.ProviderRow{
				ID: id.New("prov"), Name: prof.Name, Preset: sql.NullString{String: prof.Preset, Valid: prof.Preset != ""},
				BaseURL: prof.BaseURL, AuthEnv: prof.AuthEnv, Model: prof.Model,
				OpusModel: prof.OpusModel, SonnetModel: prof.SonnetModel, HaikuModel: prof.HaikuModel,
				FableModel: prof.FableModel, SmallFastModel: prof.SmallFastModel, SubagentModel: prof.SubagentModel,
				ExtraEnvJSON: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
			}
			if err := a.Store.UpsertProvider(ctx, row); err != nil {
				return db.Task{}, err
			}
			// Upsert on name conflict keeps old id — re-read
			if p2, err := a.Store.GetProviderByName(ctx, prof.Name); err == nil {
				providerID = sql.NullString{String: p2.ID, Valid: true}
			} else {
				providerID = sql.NullString{String: row.ID, Valid: true}
			}
		}
	}
	next, err := a.Sched.ComputeInitialNext(cronExpr)
	if err != nil {
		return db.Task{}, fmt.Errorf("cron %q: %w", cronExpr, err)
	}
	if workspace == "" {
		workspace = a.Cfg.DataDir
	}
	task := db.Task{
		ID: id.New("task"), Name: name, Status: "active", Prompt: prompt,
		WorkspaceDir: workspace, ProviderID: providerID, CronExpr: cronExpr,
		OverlapPolicy: "skip", TimeoutMinutes: int(a.Cfg.DefaultTimeout / time.Minute),
		NextRunAt: sql.NullString{String: next.UTC().Format(time.RFC3339), Valid: true},
		CreatedAt: now, UpdatedAt: now,
	}
	if task.TimeoutMinutes <= 0 {
		task.TimeoutMinutes = 30
	}
	if err := a.Store.InsertTask(ctx, task); err != nil {
		return db.Task{}, err
	}
	return task, nil
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
			Preset: sql.NullString{String: p.Preset, Valid: p.Preset != ""},
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

// HomeFromEnv helps tests override config home quickly.
func HomeFromEnv() string {
	if v := os.Getenv("TIDEBOT_HOME"); v != "" {
		return v
	}
	return ""
}
