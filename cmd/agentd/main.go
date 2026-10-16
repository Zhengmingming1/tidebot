// Command agentd is the tidebot core daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/ipc"

	"github.com/Zhengmingming1/tidebot/internal/app"
	"github.com/Zhengmingming1/tidebot/internal/config"
)

func main() {
	if guard.IsHookInvocation(os.Args[1:]) {
		guard.HookMain(os.Stdin, os.Stdout)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("tidebot agentd", buildinfo.Version, buildinfo.Commit)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		restoreMain(os.Args[2:])
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

// restoreMain: agentd restore [-config x] [-passphrase-env VAR] <file.agentbak>
// Run it with agentd stopped. The passphrase is read from an env var, never argv.
func restoreMain(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.yaml/json")
	passEnv := fs.String("passphrase-env", "TIDEBOT_BACKUP_PASSPHRASE", "env var holding the backup passphrase")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: agentd restore [-config path] <file.agentbak>")
		os.Exit(2)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if c, err := ipc.Dial(cfg.IPCPath); err == nil {
		c.Close()
		fmt.Fprintln(os.Stderr, "agentd is running; stop it before restoring")
		os.Exit(1)
	}
	m, err := app.RestoreBackup(fs.Arg(0), cfg.HomeDir, os.Getenv(*passEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("restored backup from %s (app %s, secrets included: %v)\n", m.CreatedAt, m.AppVersion, m.IncludesSecrets)
}
