// Command agentctl talks to a running agentd over local IPC (JSON-RPC).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Zhengmingming1/tidebot/internal/config"
	"github.com/Zhengmingming1/tidebot/internal/i18n"
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
		fatal(fmt.Errorf("%s", i18n.T("cli.connect", cfg.IPCPath, err)))
	}
	defer cli.Close()

	switch cmd {
	case "call":
		// Generic escape hatch: agentctl call <method> ['{"json":"params"}']
		if len(args) == 0 {
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl call <method> [json-params]")))
		}
		var params any
		if len(args) > 1 {
			if err := json.Unmarshal([]byte(args[1]), &params); err != nil {
				fatal(err)
			}
		}
		var out any
		must(cli.Call(args[0], params, &out))
		printJSON(out)

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
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl tasks ls|get|create|pause|resume|run|versions|rollback|tag")))
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
			fmt.Println(i18n.T("cli.ok"))
		case "resume":
			must(cli.Call("tasks.resume", map[string]string{"id": needFlag(rest, "--id")}, nil))
			fmt.Println(i18n.T("cli.ok"))
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
				fmt.Println(i18n.T("cli.ok"))
			default:
				fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl spend summary|set-budget")))
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
				fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl webui [on|off|status]")))
			}

		case "auth":
			if len(args) == 0 {
				fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl auth set-password|totp-setup|totp-disable")))
			}
			switch args[0] {
			case "set-password":
				pw := flagOpt(args[1:], "--password", "")
				if pw == "" {
					fatal(fmt.Errorf("%s", i18n.T("cli.required", "--password")))
				}
				must(cli.Call("auth.set_password", map[string]string{"password": pw}, nil))
				fmt.Println(i18n.T("cli.ok"))
			case "totp-setup":
				p := map[string]string{"secret": flagOpt(args[1:], "--secret", ""), "code": flagOpt(args[1:], "--code", "")}
				var out any
				must(cli.Call("auth.totp_setup", p, &out))
				printJSON(out)
			case "totp-disable":
				must(cli.Call("auth.totp_disable", nil, nil))
				fmt.Println(i18n.T("cli.ok"))
			default:
				fatal(fmt.Errorf("%s", i18n.T("cli.unknown_sub", "auth", args[0])))
			}

		case "run":
			must(cli.Call("tasks.run", map[string]string{"id": needFlag(rest, "--id")}, nil))
			fmt.Println(i18n.T("cli.ok"))
		case "versions":
			var out any
			must(cli.Call("tasks.versions", map[string]string{"id": needFlag(rest, "--id")}, &out))
			printJSON(out)
		case "rollback":
			must(cli.Call("tasks.rollback", map[string]string{
				"id": needFlag(rest, "--id"), "version_id": needFlag(rest, "--version"), "reason": flagOpt(rest, "--reason", "rollback"),
			}, nil))
			fmt.Println(i18n.T("cli.ok"))
		case "tag":
			must(cli.Call("tasks.tag", map[string]string{
				"id": needFlag(rest, "--id"), "tag": needFlag(rest, "--tag"), "version_id": needFlag(rest, "--version"),
			}, nil))
			fmt.Println(i18n.T("cli.ok"))
		default:
			fatal(fmt.Errorf("%s", i18n.T("cli.unknown_sub", "tasks", sub)))
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
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl amend propose|list|confirm|reject|resolve")))
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
			must(cli.Call("amendments.confirm", amendTarget(args[1:]), &out))
			printJSON(out)
		case "reject":
			var out any
			must(cli.Call("amendments.reject", amendTarget(args[1:]), &out))
			if out != nil {
				printJSON(out)
			} else {
				fmt.Println(i18n.T("cli.ok"))
			}
		case "resolve":
			must(cli.Call("amendments.resolve", map[string]string{
				"id": needFlag(args[1:], "--id"), "resolution": needFlag(args[1:], "--resolution"),
			}, nil))
			fmt.Println(i18n.T("cli.ok"))
		default:
			fatal(fmt.Errorf("%s", i18n.T("cli.unknown_sub", "amend", args[0])))
		}

	case "skills":
		if len(args) == 0 {
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl skills import|list|rollback|pin")))
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
			fmt.Println(i18n.T("cli.ok"))
		case "pin":
			must(cli.Call("skills.pin", map[string]string{
				"task_id": needFlag(args[1:], "--task"), "skill_id": needFlag(args[1:], "--skill"),
				"skill_version_id": needFlag(args[1:], "--version"),
			}, nil))
			fmt.Println(i18n.T("cli.ok"))
		default:
			fatal(fmt.Errorf("%s", i18n.T("cli.unknown_sub", "skills", args[0])))
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
			fmt.Println(i18n.T("cli.ok"))
		default:
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl spend summary|set-budget")))
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
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl webui [on|off|status]")))
		}

	case "auth":
		if len(args) == 0 {
			fatal(fmt.Errorf("%s", i18n.T("cli.usage_line", "agentctl auth set-password|totp-setup|totp-disable")))
		}
		switch args[0] {
		case "set-password":
			pw := flagOpt(args[1:], "--password", "")
			if pw == "" {
				fatal(fmt.Errorf("%s", i18n.T("cli.required", "--password")))
			}
			must(cli.Call("auth.set_password", map[string]string{"password": pw}, nil))
			fmt.Println(i18n.T("cli.ok"))
		case "totp-setup":
			p := map[string]string{"secret": flagOpt(args[1:], "--secret", ""), "code": flagOpt(args[1:], "--code", "")}
			var out any
			must(cli.Call("auth.totp_setup", p, &out))
			printJSON(out)
		case "totp-disable":
			must(cli.Call("auth.totp_disable", nil, nil))
			fmt.Println(i18n.T("cli.ok"))
		default:
			fatal(fmt.Errorf("%s", i18n.T("cli.unknown_sub", "auth", args[0])))
		}

	case "run":
		// Ad-hoc run still goes through daemon via a one-shot task create+run is heavy;
		// expose as tasks.create then tasks.run, or keep a thin status-only CLI.
		fatal(fmt.Errorf("%s", i18n.T("cli.use_tasks")))

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, i18n.T("cli.usage"))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, i18n.T("cli.error", err))
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

// amendTarget parses --batch <amendment_id> [--only id1,id2] or --id <id[,id...]>.
func amendTarget(args []string) map[string]any {
	if b := flagOpt(args, "--batch", ""); b != "" {
		m := map[string]any{"batch": b}
		if o := flagOpt(args, "--only", ""); o != "" {
			m["only"] = strings.Split(o, ",")
		}
		return m
	}
	return map[string]any{"id": needFlag(args, "--id")}
}

func needFlag(args []string, name string) string {
	v := flagOpt(args, name, "")
	if v == "" {
		fatal(fmt.Errorf("%s", i18n.T("cli.required", name)))
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
	fatal(fmt.Errorf("%s", i18n.T("cli.required", name)))
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
