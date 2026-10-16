// Command agentd is the tidebot core daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"os"
	"os/signal"
	"syscall"

	"github.com/Zhengmingming1/tidebot/internal/app"
	"github.com/Zhengmingming1/tidebot/internal/config"
)

func main() {
	if guard.IsHookInvocation(os.Args[1:]) {
		guard.HookMain(os.Stdin, os.Stdout)
		return
	}
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

	d, err := a.StartDaemon(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}
	select {
	case <-ctx.Done():
		a.Log.Info("agentd shutting down")
	case err := <-d.Errs:
		if err != nil {
			a.Log.Error("ipc server", "error", err)
		}
	}
	d.Stop()
}
