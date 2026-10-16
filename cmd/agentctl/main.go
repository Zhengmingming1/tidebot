// Command agentctl is the Phase-0 CLI: run once, list runs, list providers.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"text/tabwriter"

	"github.com/Zhengmingming1/tidebot/internal/app"
	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	cfg, err := config.Load(os.Getenv("TIDEBOT_CONFIG"))
	if err != nil {
		fatal(err)
	}
	a, err := app.Open(cfg)
	if err != nil {
		fatal(err)
	}
	defer a.Close()

	ctx := context.Background()
	switch cmd {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		prompt := fs.String("prompt", "", "prompt to send to Claude Code")
		providerName := fs.String("provider", cfg.DefaultProvider, "provider profile name")
		workspace := fs.String("workspace", "", "working directory")
		timeout := fs.Duration("timeout", cfg.DefaultTimeout, "run timeout")
		saveEvents := fs.String("save-events", "", "optional path to write sanitized stream-json events")
		_ = fs.Parse(args)
		if *prompt == "" {
			fatal(fmt.Errorf("--prompt is required"))
		}
		res, err := a.RunPrompt(ctx, *prompt, *workspace, *providerName, "", "manual", *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run finished with error: %v (status=%s run_id=%s)\n", err, res.Status, res.RunID)
			if *saveEvents != "" {
				_ = writeSanitizedEvents(*saveEvents, res)
			}
			os.Exit(1)
		}
		fmt.Printf("run_id=%s status=%s session=%s cost_usd=%.6f tokens_in=%d tokens_out=%d duration_ms=%d\n",
			res.RunID, res.Status, res.SessionID, res.TotalCostUSD, res.InputTokens, res.OutputTokens, res.DurationMS)
		if res.ResultText != "" {
			fmt.Println(res.ResultText)
		}
		if *saveEvents != "" {
			if err := writeSanitizedEvents(*saveEvents, res); err != nil {
				fatal(err)
			}
			fmt.Fprintf(os.Stderr, "wrote sanitized events to %s\n", *saveEvents)
		}

	case "runs":
		fs := flag.NewFlagSet("runs", flag.ExitOnError)
		taskID := fs.String("task", "", "filter by task id")
		limit := fs.Int("limit", 20, "max rows")
		_ = fs.Parse(args)
		list, err := a.Store.ListRuns(ctx, *taskID, *limit)
		if err != nil {
			fatal(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tPROVIDER\tMODEL\tCOST\tCREATED")
		for _, r := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%.4f\t%s\n",
				r.ID, r.Status, r.ProviderName.String, r.Model.String, r.TotalCostUSD.Float64, r.CreatedAt)
		}
		_ = tw.Flush()

	case "providers":
		sub := "list"
		if len(args) > 0 {
			sub = args[0]
			args = args[1:]
		}
		switch sub {
		case "list", "ls":
			_ = a.SeedBuiltinProviders(ctx)
			fmt.Println("Built-in / configured providers:")
			for _, p := range a.Registry.List() {
				base := p.BaseURL
				if base == "" {
					base = "(anthropic default)"
				}
				fmt.Printf("- %-18s  base=%s  model=%s  auth=%s\n", p.Name, base, p.Model, p.AuthEnv)
			}
			fmt.Println("\nVerified preset sources:")
			for _, preset := range provider.BuiltIn() {
				fmt.Printf("- %-18s  %s\n", preset.ID, preset.SourceURL)
			}
		default:
			fatal(fmt.Errorf("unknown providers subcommand %q", sub))
		}

	case "task":
		if len(args) == 0 {
			fatal(fmt.Errorf("usage: agentctl task create ..."))
		}
		switch args[0] {
		case "create":
			fs := flag.NewFlagSet("task create", flag.ExitOnError)
			name := fs.String("name", "", "task name")
			prompt := fs.String("prompt", "", "prompt")
			cronExpr := fs.String("cron", "", "cron expression (5-field)")
			prov := fs.String("provider", cfg.DefaultProvider, "provider")
			ws := fs.String("workspace", "", "workspace")
			_ = fs.Parse(args[1:])
			if *name == "" || *prompt == "" || *cronExpr == "" {
				fatal(fmt.Errorf("--name --prompt --cron required"))
			}
			task, err := a.EnsureTask(ctx, *name, *prompt, *cronExpr, *prov, *ws)
			if err != nil {
				fatal(err)
			}
			fmt.Printf("created task id=%s next_run_at=%s\n", task.ID, task.NextRunAt.String)
		default:
			fatal(fmt.Errorf("unknown task subcommand"))
		}

	case "version":
		fmt.Println("tidebot agentctl phase0")

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `agentctl — tidebot CLI (phase 0)

Usage:
  agentctl run --prompt "..." [--provider NAME] [--workspace DIR] [--timeout 30m]
  agentctl runs [--task ID] [--limit 20]
  agentctl providers list
  agentctl task create --name N --prompt P --cron "*/5 * * * *" [--provider NAME]
  agentctl version
`)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}


func writeSanitizedEvents(path string, res runner.Result) error {
	reUUID := regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reSess := regexp.MustCompile(`sess_[A-Za-z0-9_-]+`)
	reKeyish := regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{8,}|Bearer\s+[A-Za-z0-9._\-]+)`)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, ev := range res.Events {
		raw := string(ev.Raw)
		if raw == "" {
			b, mErr := json.Marshal(ev)
			if mErr != nil {
				continue
			}
			raw = string(b)
		}
		raw = reUUID.ReplaceAllString(raw, "00000000-0000-0000-0000-000000000000")
		raw = reSess.ReplaceAllString(raw, "sess_REDACTED")
		raw = reKeyish.ReplaceAllString(raw, "***")
		if _, err := f.WriteString(raw + "\n"); err != nil {
			return err
		}
	}
	return nil
}
