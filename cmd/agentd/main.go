// Command agentd is the tidebot core daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/plugin/official"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/ipc"
	"github.com/Zhengmingming1/tidebot/internal/updater"

	"github.com/Zhengmingming1/tidebot/internal/app"
	"github.com/Zhengmingming1/tidebot/internal/config"
)

func main() {
	if guard.IsHookInvocation(os.Args[1:]) {
		guard.HookMain(os.Stdin, os.Stdout)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == plugin.BuiltinArg {
		os.Exit(official.Main(os.Args[2:]))
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

	// A previous self-update may be waiting for this build to prove itself.
	// After MaxAttempts failed starts Boot restores the backup; re-exec it.
	if bs := updater.Boot(cfg.HomeDir); bs.RolledBack {
		fmt.Fprintln(os.Stderr, "update: new version failed to start; rolled back")
		reexec()
	}

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
		// Another core already serving is not this build's fault: no rollback.
		if !errors.Is(err, ipc.ErrInUse) && updater.RollbackNow(cfg.HomeDir, "start: "+err.Error()) == nil {
			a.Close()
			reexec()
		}
		os.Exit(1)
	}
	restart := make(chan struct{}, 1)
	a.SetRestartFunc(func() { restart <- struct{}{} })
	go confirmWhenHealthy(cfg, a)
	select {
	case <-ctx.Done():
		// Restore default signal handling: a second Ctrl+C / SIGTERM quits at once
		// (runs left behind are closed by the next start's recovery).
		cancel()
		a.Log.Info("agentd shutting down", "running_runs", a.Runner.Active(),
			"grace", cfg.ShutdownGrace.String(), "hint", "send the signal again to quit now")
	case <-restart:
		a.Log.Info("agentd restarting into the new version")
		d.Stop()
		a.Close()
		if updater.UnderServiceManager() {
			os.Exit(app.ExitCodeRestart)
		}
		exe := updater.Executable()
		healthy := func() bool { return pingStatus(cfg.IPCPath) && !updater.IsPending(cfg.HomeDir) }
		if err := updater.Handover(exe, os.Args[1:], cfg.HomeDir, healthy, 90*time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "update handover: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
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

// confirmWhenHealthy pings our own IPC socket; success clears the update marker.
// If the new build can't answer within a minute, roll back and restart.
func confirmWhenHealthy(cfg config.Config, a *app.App) {
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if pingStatus(cfg.IPCPath) {
			if r := updater.Confirm(cfg.HomeDir); r != nil {
				a.Log.Info("update confirmed", "from", r.From, "to", r.To)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
	if updater.RollbackNow(cfg.HomeDir, "health check timed out") == nil {
		a.Log.Error("update health check failed; rolled back")
		reexec()
	}
}

// reexec hands over to whatever binary is now at our path. Under systemd we
// just exit and let Restart=on-failure start it; otherwise we spawn it detached.
func reexec() {
	if updater.UnderServiceManager() {
		os.Exit(app.ExitCodeRestart)
	}
	err := updater.Relaunch(updater.Executable(), os.Args[1:])
	if err == nil {
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "restart failed: %v; start agentd again\n", err)
	os.Exit(1)
}

func pingStatus(path string) bool {
	c, err := ipc.DialClient(path)
	if err != nil {
		return false
	}
	defer c.Close()
	var out map[string]any
	return c.Call("status", nil, &out) == nil
}
