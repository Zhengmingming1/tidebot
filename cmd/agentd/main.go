// Command agentd is the tidebot core daemon.
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
	"github.com/Zhengmingming1/tidebot/internal/ipc"
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

	ln, err := ipc.Listen(cfg.IPCPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ipc listen: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(cfg.IPCPath)
	}()

	srv := ipc.NewServer(a.Log)
	a.RegisterRPC(srv)

	a.Log.Info("agentd starting",
		"db", cfg.DBPath,
		"ipc", cfg.IPCPath,
		"max_concurrency", cfg.MaxConcurrency,
		"claude_bin", cfg.ClaudeBin,
		"default_provider", cfg.DefaultProvider,
		"webui", cfg.EffectiveWebUI(),
	)

	a.Sched.Start(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx, ln) }()

	select {
	case <-ctx.Done():
		a.Log.Info("agentd shutting down")
	case err := <-errCh:
		if err != nil {
			a.Log.Error("ipc server", "error", err)
		}
	}
	a.Sched.Stop()
	time.Sleep(100 * time.Millisecond)
}
