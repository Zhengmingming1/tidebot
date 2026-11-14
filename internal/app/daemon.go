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
	a              *App
	Errs           <-chan error
	close          func()
	stopBackground context.CancelFunc
	once           sync.Once
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
	if err := a.recoverSkillSetEvals(ctx); err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := a.recoverInterruptedRuns(ctx); err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := a.recoverWorkflows(ctx); err != nil {
		_ = ln.Close()
		return nil, err
	}
	// Stop owns background-loop cancellation even if the caller keeps ctx alive.
	ctx, stopBackground := context.WithCancel(ctx)
	var background sync.WaitGroup
	startBackground := func(fn func(context.Context)) {
		background.Add(1)
		go func() { defer background.Done(); fn(ctx) }()
	}
	a.purgeSkillStages()
	a.purgeRunMCP()
	a.purgeRunSandbox()
	a.recoverChatSessions(ctx)
	a.recoverCapabilityProposals(ctx)
	a.purgeRunSnaps()
	a.cleanLegacySkillCopies(ctx)
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
	if n, err := a.Sched.Realign(ctx, time.Now()); err != nil {
		a.Log.Error("realign schedules", "error", err)
	} else if n > 0 {
		a.Log.Info("realigned schedules", "tasks", n)
	}
	a.Sched.Start(ctx)
	startBackground(a.runNotifier)
	startBackground(a.runOptimizer)
	startBackground(a.PluginHost.Run)
	startBackground(func(ctx context.Context) { a.runFileWatcher(ctx, 0) })
	startBackground(a.runRetention)
	startBackground(a.runDiskWatch)
	startBackground(a.runUpdateChecker)
	startBackground(a.runPriceCatalog)
	if os.Getenv("TIDEBOT_NO_AUTO_BACKUP") != "1" {
		startBackground(a.runAutoBackup)
	}
	if v := a.claudeVersion(); !ClaudeCompatible(v) {
		a.Log.Warn("claude code is older than tested", "version", v, "min", MinClaudeVersion)
	}
	// IPC requests (e.g. a blocking `agentctl tasks run`) must not be canceled the
	// moment agentd gets SIGTERM: Stop drains runs first, then closes IPC.
	serveCtx, stopServe := context.WithCancel(context.WithoutCancel(ctx))
	errCh := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); errCh <- srv.Serve(serveCtx, ln) }()
	return &Daemon{a: a, Errs: errCh, stopBackground: stopBackground, close: func() {
		stopBackground()
		stopServe()
		_ = ln.Close()
		<-serveDone
		backgroundDone := make(chan struct{})
		go func() { background.Wait(); close(backgroundDone) }()
		select {
		case <-backgroundDone:
		case <-time.After(shutdownWriteWait):
			a.Log.Warn("background shutdown timed out")
		}
		_ = os.Remove(a.Cfg.IPCPath)
	}}, nil
}

// Stop shuts the core down: no new scheduled runs, then running runs get
// Cfg.ShutdownGrace to finish (see drainRuns), then the WebUI and IPC close.
// IPC stays up while draining so a second core can't start and "recover" runs
// that are still writing. Safe to call more than once.
func (d *Daemon) Stop() {
	d.once.Do(func() {
		d.stopBackground()
		d.a.Sched.Stop()
		d.a.stopPolicyRuns()
		d.a.stopSkillSetEvals()
		d.a.stopWorkflows()
		d.a.drainRuns()
		d.a.waitWorkflows()
		d.a.waitSkillSetEvals()
		d.a.waitPolicyRuns()
		d.a.flushLedgerOnQuit()
		// Bounded: an open live-log stream (SSE) never goes idle on its own.
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = d.a.WebUI.Stop(sctx)
		cancel()
		d.close()
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
