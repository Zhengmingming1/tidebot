// Command agentd is the tidebot core daemon (Phase 0 skeleton).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/app"
	"github.com/Zhengmingming1/tidebot/internal/config"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml/json")
	noWebUI := flag.Bool("no-webui", false, "disable HTTP WebUI listener (D8)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	cfg.NoWebUI = *noWebUI

	a, err := app.Open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.SeedBuiltinProviders(ctx); err != nil {
		a.Log.Error("seed providers", "error", err)
	}

	a.Log.Info("agentd starting",
		"db", cfg.DBPath,
		"max_concurrency", cfg.MaxConcurrency,
		"claude_bin", cfg.ClaudeBin,
		"default_provider", cfg.DefaultProvider,
		"webui", cfg.EffectiveWebUI(),
	)

	a.Sched.Start(ctx)

	// Phase 0: no HTTP yet; idle wait. WebUI toggle is reserved for later stages.
	if cfg.EffectiveWebUI() {
		a.Log.Info("webui enabled in config but HTTP server not implemented in phase 0")
	}

	<-ctx.Done()
	a.Log.Info("agentd shutting down")
	a.Sched.Stop()
	// give in-flight runs a moment (Phase 0: best-effort)
	time.Sleep(100 * time.Millisecond)
}
