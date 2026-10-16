// Command agentctl talks to a running agentd over local IPC (JSON-RPC).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/ipc"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	if cmd == "version" {
		fmt.Println("tidebot agentctl phase1")
		return
	}

	cfg, err := config.Load(os.Getenv("TIDEBOT_CONFIG"))
	if err != nil {
		fatal(err)
	}
	cli, err := ipc.DialClient(cfg.IPCPath)
	if err != nil {
		fatal(fmt.Errorf("connect agentd at %s: %w (is agentd running?)", cfg.IPCPath, err))
	}
	defer cli.Close()

	switch cmd {
	case "status":
		var out map[string]any
		must(cli.Call("status", nil, &out))
		printJSON(out)

	case "providers":
		var out any
		must(cli.Call("providers.list", nil, &out))
		printJSON(out)

	case "tasks":
		if len(args) == 0 {
			fatal(fmt.Errorf("usage: agentctl tasks ls|get|create|pause|resume|run|versions|rollback|tag"))
		}
		sub := args[0]
		rest := args[1:]
		switch sub {
		case "ls", "list":
			var out any
			must(cli.Call("tasks.list", nil, &out))
			printJSON(out)
		case "get":
			id := flagVal(rest, "--id", 0)
			var out any
			must(cli.Call("tasks.get", map[string]string{"id": id}, &out))
			printJSON(out)
		case "create":
			p := map[string]any{
				"name":          needFlag(rest, "--name"),
				"prompt":        needFlag(rest, "--prompt"),
				"cron_expr":     flagOpt(rest, "--cron", ""),
				"provider_name": flagOpt(rest, "--provider", cfg.DefaultProvider),
				"workspace_dir": flagOpt(rest, "--workspace", ""),
			}
			var out any
			must(cli.Call("tasks.create", p, &out))
			printJSON(out)
		case "pause":
			must(cli.Call("tasks.pause", map[string]string{"id": needFlag(rest, "--id")}, nil))
			fmt.Println("ok")
		case "resume":
			must(cli.Call("tasks.resume", map[string]string{"id": needFlag(rest, "--id")}, nil))
			fmt.Println("ok")
		case "spend":
			sub := "summary"
			if len(args) > 0 {
				sub = args[0]
			}
			switch sub {
			case "summary", "ls", "list":
				var out any
				must(cli.Call("spend.summary", map[string]string{"day": flagOpt(args, "--day", "")}, &out))
				printJSON(out)
			case "set-budget":
				p := map[string]any{
					"scope":      flagOpt(args, "--scope", "global"),
					"scope_id":   flagOpt(args, "--scope-id", ""),
					"period":     flagOpt(args, "--period", "day"),
					"bucket":     flagOpt(args, "--bucket", ""),
					"max_usd":    atof(flagOpt(args, "--max-usd", "0")),
					"max_tokens": atoi(flagOpt(args, "--max-tokens", "0")),
				}
				must(cli.Call("spend.set_budget", p, nil))
				fmt.Println("ok")
			default:
				fatal(fmt.Errorf("usage: agentctl spend summary|set-budget"))
			}

		case "webui":
			if len(args) == 0 {
				var out any
				must(cli.Call("webui.status", nil, &out))
				printJSON(out)
				break
			}
			switch args[0] {
			case "on", "enable":
				var out any
				must(cli.Call("webui.set", map[string]any{"enabled": true}, &out))
				printJSON(out)
			case "off", "disable":
				var out any
				must(cli.Call("webui.set", map[string]any{"enabled": false}, &out))
				printJSON(out)
			case "status":
				var out any
				must(cli.Call("webui.status", nil, &out))
				printJSON(out)
			default:
				fatal(fmt.Errorf("usage: agentctl webui [on|off|status]"))
			}

		case "auth":
			if len(args) == 0 {
				fatal(fmt.Errorf("usage: agentctl auth set-password|totp-setup|totp-disable"))
			}
			switch args[0] {
			case "set-password":
				pw := flagOpt(args[1:], "--password", "")
				if pw == "" {
					fatal(fmt.Errorf("--password required"))
				}
				must(cli.Call("auth.set_password", map[string]string{"password": pw}, nil))
				fmt.Println("ok")
			case "totp-setup":
				p := map[string]string{"secret": flagOpt(args[1:], "--secret", ""), "code": flagOpt(args[1:], "--code", "")}
				var out any
				must(cli.Call("auth.totp_setup", p, &out))
				printJSON(out)
			case "totp-disable":
				must(cli.Call("auth.totp_disable", nil, nil))
				fmt.Println("ok")
			default:
				fatal(fmt.Errorf("unknown auth subcommand"))
			}

		case "run":
			must(cli.Call("tasks.run", map[string]string{"id": needFlag(rest, "--id")}, nil))
			fmt.Println("ok")
		case "versions":
			var out any
			must(cli.Call("tasks.versions", map[string]string{"id": needFlag(rest, "--id")}, &out))
			printJSON(out)
		case "rollback":
			must(cli.Call("tasks.rollback", map[string]string{
				"id": needFlag(rest, "--id"), "version_id": needFlag(rest, "--version"), "reason": flagOpt(rest, "--reason", "rollback"),
			}, nil))
			fmt.Println("ok")
		case "tag":
			must(cli.Call("tasks.tag", map[string]string{
				"id": needFlag(rest, "--id"), "tag": needFlag(rest, "--tag"), "version_id": needFlag(rest, "--version"),
			}, nil))
			fmt.Println("ok")
		default:
			fatal(fmt.Errorf("unknown tasks subcommand %q", sub))
		}

	case "runs":
		p := map[string]any{"task_id": flagOpt(args, "--task", ""), "limit": 20}
		var out []map[string]any
		must(cli.Call("runs.list", p, &out))
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tPROVIDER\tCREATED")
		for _, r := range out {
			fmt.Fprintf(tw, "%v\t%v\t%v\t%v\n", r["ID"], r["Status"], r["ProviderName"], r["CreatedAt"])
		}
		_ = tw.Flush()

	case "amend":
		if len(args) == 0 {
			fatal(fmt.Errorf("usage: agentctl amend propose|list|confirm|reject|resolve"))
		}
		switch args[0] {
		case "propose":
			var out any
			must(cli.Call("amendments.propose", map[string]string{
				"task_id": needFlag(args[1:], "--task"), "utterance": needFlag(args[1:], "--text"),
			}, &out))
			printJSON(out)
		case "list":
			var out any
			must(cli.Call("amendments.list", map[string]string{
				"task_id": needFlag(args[1:], "--task"), "status": flagOpt(args[1:], "--status", ""),
			}, &out))
			printJSON(out)
		case "confirm":
			var out any
			must(cli.Call("amendments.confirm", map[string]string{"id": needFlag(args[1:], "--id")}, &out))
			printJSON(out)
		case "reject":
			must(cli.Call("amendments.reject", map[string]string{"id": needFlag(args[1:], "--id")}, nil))
			fmt.Println("ok")
		case "resolve":
			must(cli.Call("amendments.resolve", map[string]string{
				"id": needFlag(args[1:], "--id"), "resolution": needFlag(args[1:], "--resolution"),
			}, nil))
			fmt.Println("ok")
		default:
			fatal(fmt.Errorf("unknown amend subcommand"))
		}

	case "skills":
		if len(args) == 0 {
			fatal(fmt.Errorf("usage: agentctl skills import|list|rollback|pin"))
		}
		switch args[0] {
		case "import":
			var out any
			must(cli.Call("skills.import", map[string]string{
				"name": flagOpt(args[1:], "--name", ""), "dir": needFlag(args[1:], "--dir"),
			}, &out))
			printJSON(out)
		case "list", "ls":
			var out any
			must(cli.Call("skills.list", nil, &out))
			printJSON(out)
		case "rollback":
			must(cli.Call("skills.rollback", map[string]string{
				"id": needFlag(args[1:], "--id"), "version_id": needFlag(args[1:], "--version"),
			}, nil))
			fmt.Println("ok")
		case "pin":
			must(cli.Call("skills.pin", map[string]string{
				"task_id": needFlag(args[1:], "--task"), "skill_id": needFlag(args[1:], "--skill"),
				"skill_version_id": needFlag(args[1:], "--version"),
			}, nil))
			fmt.Println("ok")
		default:
			fatal(fmt.Errorf("unknown skills subcommand"))
		}

	case "spend":
		sub := "summary"
		if len(args) > 0 {
			sub = args[0]
		}
		switch sub {
		case "summary", "ls", "list":
			var out any
			must(cli.Call("spend.summary", map[string]string{"day": flagOpt(args, "--day", "")}, &out))
			printJSON(out)
		case "set-budget":
			p := map[string]any{
				"scope":      flagOpt(args, "--scope", "global"),
				"scope_id":   flagOpt(args, "--scope-id", ""),
				"period":     flagOpt(args, "--period", "day"),
				"bucket":     flagOpt(args, "--bucket", ""),
				"max_usd":    atof(flagOpt(args, "--max-usd", "0")),
				"max_tokens": atoi(flagOpt(args, "--max-tokens", "0")),
			}
			must(cli.Call("spend.set_budget", p, nil))
			fmt.Println("ok")
		default:
			fatal(fmt.Errorf("usage: agentctl spend summary|set-budget"))
		}

	case "webui":
		if len(args) == 0 {
			var out any
			must(cli.Call("webui.status", nil, &out))
			printJSON(out)
			break
		}
		switch args[0] {
		case "on", "enable":
			var out any
			must(cli.Call("webui.set", map[string]any{"enabled": true}, &out))
			printJSON(out)
		case "off", "disable":
			var out any
			must(cli.Call("webui.set", map[string]any{"enabled": false}, &out))
			printJSON(out)
		case "status":
			var out any
			must(cli.Call("webui.status", nil, &out))
			printJSON(out)
		default:
			fatal(fmt.Errorf("usage: agentctl webui [on|off|status]"))
		}

	case "auth":
		if len(args) == 0 {
			fatal(fmt.Errorf("usage: agentctl auth set-password|totp-setup|totp-disable"))
		}
		switch args[0] {
		case "set-password":
			pw := flagOpt(args[1:], "--password", "")
			if pw == "" {
				fatal(fmt.Errorf("--password required"))
			}
			must(cli.Call("auth.set_password", map[string]string{"password": pw}, nil))
			fmt.Println("ok")
		case "totp-setup":
			p := map[string]string{"secret": flagOpt(args[1:], "--secret", ""), "code": flagOpt(args[1:], "--code", "")}
			var out any
			must(cli.Call("auth.totp_setup", p, &out))
			printJSON(out)
		case "totp-disable":
			must(cli.Call("auth.totp_disable", nil, nil))
			fmt.Println("ok")
		default:
			fatal(fmt.Errorf("unknown auth subcommand"))
		}

	case "run":
		// Ad-hoc run still goes through daemon via a one-shot task create+run is heavy;
		// expose as tasks.create then tasks.run, or keep a thin status-only CLI.
		fatal(fmt.Errorf("use: agentctl tasks create ... && agentctl tasks run --id <id>"))

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `agentctl — talks to agentd over local Unix socket

  agentctl status
  agentctl providers
  agentctl tasks ls|get|create|pause|resume|run|versions|rollback|tag
  agentctl runs [--task ID]
  agentctl amend propose|list|confirm|reject|resolve
  agentctl skills import|list|rollback|pin
  agentctl spend summary|set-budget
  agentctl webui [on|off|status]
  agentctl auth set-password|totp-setup|totp-disable
  agentctl version

IPC path: $TIDEBOT_HOME/agentd.sock (or config ipc_path / TIDEBOT_IPC)
`)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func needFlag(args []string, name string) string {
	v := flagOpt(args, name, "")
	if v == "" {
		fatal(fmt.Errorf("%s required", name))
	}
	return v
}

func flagOpt(args []string, name, def string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
		if len(args[i]) > len(name)+1 && args[i][:len(name)+1] == name+"=" {
			return args[i][len(name)+1:]
		}
	}
	return def
}

func flagVal(args []string, name string, pos int) string {
	if v := flagOpt(args, name, ""); v != "" {
		return v
	}
	if pos >= 0 && pos < len(args) && args[pos] != "" && args[pos][0] != '-' {
		return args[pos]
	}
	fatal(fmt.Errorf("%s required", name))
	return ""
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func atoi(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
