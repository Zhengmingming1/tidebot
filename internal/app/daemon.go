package app

import (
	"context"
	"os"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

// Daemon is a running core: IPC server, optional TCP WebUI and the scheduler.
// Used by both agentd and the desktop shell (which embeds the core in-process).
type Daemon struct {
	a     *App
	Errs  <-chan error
	close func()
}

// StartDaemon seeds providers, starts IPC, the WebUI listener (if enabled) and the scheduler.
func (a *App) StartDaemon(ctx context.Context) (*Daemon, error) {
	if err := a.SeedBuiltinProviders(ctx); err != nil {
		a.Log.Error("seed providers", "error", err)
	}
	a.LoadNetwork(ctx)
	ln, err := ipc.Listen(a.Cfg.IPCPath)
	if err != nil {
		return nil, err
	}
	srv := ipc.NewServer(a.Log)
	a.RegisterRPC(srv)
	a.Log.Info("core starting", "db", a.Cfg.DBPath, "ipc", a.Cfg.IPCPath,
		"max_concurrency", a.Cfg.MaxConcurrency, "default_provider", a.Cfg.DefaultProvider,
		"webui", a.Cfg.EffectiveWebUI(), "webui_addr", a.Cfg.WebUIAddr)
	if a.Cfg.EffectiveWebUI() {
		if err := a.WebUI.Start(ctx); err != nil {
			a.Log.Error("webui start", "error", err)
		}
	}
	a.LoadPriceCatalog()
	a.Sched.Start(ctx)
	go a.runNotifier(ctx)
	go a.runOptimizer(ctx)
	go a.PluginHost.Run(ctx)
	go a.runFileWatcher(ctx, 0)
	go a.runRetention(ctx)
	go a.runUpdateChecker(ctx)
	go a.runPriceCatalog(ctx)
	if os.Getenv("TIDEBOT_NO_AUTO_BACKUP") != "1" {
		go a.runAutoBackup(ctx)
	}
	if v := a.claudeVersion(); !ClaudeCompatible(v) {
		a.Log.Warn("claude code is older than tested", "version", v, "min", MinClaudeVersion)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx, ln) }()
	return &Daemon{a: a, Errs: errCh, close: func() {
		_ = ln.Close()
		_ = os.Remove(a.Cfg.IPCPath)
	}}, nil
}

// Stop shuts the core down.
func (d *Daemon) Stop() {
	d.a.Sched.Stop()
	_ = d.a.WebUI.Stop(context.Background())
	d.close()
	time.Sleep(100 * time.Millisecond)
}
