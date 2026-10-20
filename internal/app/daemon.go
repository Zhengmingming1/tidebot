package app

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

// Daemon is a running core: IPC server, optional TCP WebUI and the scheduler.
// Used by both agentd and the desktop shell (which embeds the core in-process).
type Daemon struct {
	a     *App
	Errs  <-chan error
	close func()
	once  sync.Once
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
	// We own the instance now: close runs a crashed / force-quit core left behind.
	a.recoverInterruptedRuns(ctx)
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
	// IPC requests (e.g. a blocking `agentctl tasks run`) must not be canceled the
	// moment agentd gets SIGTERM: Stop drains runs first, then closes IPC.
	serveCtx, stopServe := context.WithCancel(context.WithoutCancel(ctx))
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(serveCtx, ln) }()
	return &Daemon{a: a, Errs: errCh, close: func() {
		stopServe()
		_ = ln.Close()
		_ = os.Remove(a.Cfg.IPCPath)
	}}, nil
}

// Stop shuts the core down: no new scheduled runs, then running runs get
// Cfg.ShutdownGrace to finish (see drainRuns), then the WebUI and IPC close.
// IPC stays up while draining so a second core can't start and "recover" runs
// that are still writing. Safe to call more than once.
func (d *Daemon) Stop() {
	d.once.Do(func() {
		d.a.Sched.Stop()
		d.a.drainRuns()
		// Bounded: an open live-log stream (SSE) never goes idle on its own.
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = d.a.WebUI.Stop(sctx)
		cancel()
		d.close()
		time.Sleep(100 * time.Millisecond)
	})
}

// shutdownWriteWait bounds how long canceled runs get to record their final
// state, so a stalled child or disk can't hold up quitting (n8n bounds its
// "record as cancelled" write the same way).
const shutdownWriteWait = 3 * time.Second

// drainRuns lets in-flight runs finish or records them as stopped-on-quit with
// the tokens they used so far, instead of leaving them "running" for the next
// start to clean up. Bounded by ShutdownGrace + shutdownWriteWait.
func (a *App) drainRuns() {
	if a.Runner == nil {
		return
	}
	n := a.Runner.Active()
	if n > 0 {
		a.Log.Info("waiting for running runs before quitting", "count", n, "grace", a.Cfg.ShutdownGrace.String())
	}
	res := a.Runner.Drain(a.Cfg.ShutdownGrace, shutdownWriteWait)
	if len(res.Stopped) > 0 {
		a.Log.Warn("stopped runs that were still going at quit", "count", len(res.Stopped), "timed_out", res.TimedOut)
	}
}
