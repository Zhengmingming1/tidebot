package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
)

// MCP for a run (capability plan §6.3): every run gets --strict-mcp-config, so
// Claude Code uses no MCP server but the ones tidebot passes, and a plugin
// connector is passed only to runs bound to it. A run is bound to a connector
// when its allowed tools, prompt or attached skills name the connector's tools
// (mcp__<plugin>__<connector>[__tool], wildcards included), or a skill's
// tidebot.json requires it as an "mcp" dependency. Runs are dontAsk, so an MCP
// tool nothing allows could never be called anyway; not starting its server
// saves a process per connector per run and keeps its secrets out of the run.

// runMCPDir holds the per-run connector configs (0600, removed when the run ends).
func (a *App) runMCPDir() string { return filepath.Join(a.Cfg.DataDir, "run-mcp") }

// mcpBinding is what a run names, matched against connectors.
type mcpBinding struct {
	refs  []string        // mcp__… tokens
	names map[string]bool // tidebot.json mcp dependency names
}

var reMCPRef = regexp.MustCompile(`mcp__[A-Za-z0-9_.*-]*`)

func (b *mcpBinding) scan(text string) {
	for _, m := range reMCPRef.FindAllString(text, -1) {
		b.refs = append(b.refs, m)
	}
}

// bound reports whether the run named server "<plugin>__<connector>".
func (b mcpBinding) bound(server, connector string) bool {
	if b.names[server] || b.names[connector] {
		return true
	}
	full := "mcp__" + server
	for _, r := range b.refs {
		if p, ok := strings.CutSuffix(r, "*"); ok {
			if p == "mcp__" || strings.HasPrefix(full+"__", p) || strings.HasPrefix(p, full+"__") {
				return true
			}
			continue
		}
		if r == full || strings.HasPrefix(r, full+"__") {
			return true
		}
	}
	return false
}

// runMCPBinding collects what the run names: its arguments (allowed tools, the
// system prompt), the prompt, system prompt files and the staged skills.
func runMCPBinding(opts *runner.Options) mcpBinding {
	b := mcpBinding{names: map[string]bool{}}
	b.scan(opts.Prompt)
	args := append(append([]string{}, opts.ExtraArgs...), opts.ContextArgs...)
	for i, arg := range args {
		b.scan(arg)
		if i+1 >= len(args) {
			continue
		}
		switch arg {
		case "--append-system-prompt-file", "--system-prompt-file":
			b.scan(readBounded(args[i+1]))
		case "--plugin-dir":
			skills, _ := filepath.Glob(filepath.Join(args[i+1], "skills", "*"))
			for _, d := range skills {
				b.scan(readBounded(filepath.Join(d, "SKILL.md")))
				if raw := readBounded(filepath.Join(d, "tidebot.json")); raw != "" {
					if m, err := skillstore.ValidateSkillManifest([]byte(raw)); err == nil {
						for _, r := range m.Environment {
							if r.Kind == "mcp" {
								b.names[r.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return b
}

func readBounded(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	raw, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(raw)
}

// mcpArgs writes the config holding the connectors bound (nil = every enabled
// one) into a fresh 0600 file and returns its arguments and a cleanup that
// removes the file; sandbox (dry) runs drop side-effect connectors.
func (a *App) mcpArgs(ctx context.Context, bucket string, bound func(server, connector string) bool, extra map[string]any) ([]string, func()) {
	nop := func() {}
	servers := map[string]any{}
	if a.Plugins != nil {
		if b, n, err := a.Plugins.MCPConfig(ctx, bucket == cost.BucketDryRun, bound); err == nil && n > 0 {
			var doc struct {
				Servers map[string]any `json:"mcpServers"`
			}
			if json.Unmarshal(b, &doc) == nil {
				for k, v := range doc.Servers {
					servers[k] = v
				}
			}
		}
	}
	for k, v := range extra {
		servers[k] = v // tidebot's own servers win a name clash
	}
	if len(servers) == 0 {
		return nil, nop
	}
	b, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return nil, nop
	}
	if err := os.MkdirAll(a.runMCPDir(), 0o700); err != nil {
		return nil, nop
	}
	f, err := os.CreateTemp(a.runMCPDir(), "mcp-*.json") // 0600
	if err != nil {
		return nil, nop
	}
	p := f.Name()
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(p)
		return nil, nop
	}
	return []string{"--mcp-config", p}, func() { _ = os.Remove(p) }
}

// applyRunMCP gives a run --strict-mcp-config, its bound connectors and the
// deny rules every run gets (agent config, sensitive reads, the fixed danger set). Call after applyGuard (which adds the task's allowed
// tools). The returned cleanup removes the run's connector config.
func (a *App) applyRunMCP(ctx context.Context, opts *runner.Options, bucket string) func() {
	cleanup := func() {}
	all := append(append([]string{}, opts.ExtraArgs...), opts.ContextArgs...)
	if !hasArg(all, "--mcp-config") {
		bound := runMCPBinding(opts).bound
		if opts.NoPluginMCP {
			bound = func(string, string) bool { return false }
		}
		var args []string
		args, cleanup = a.mcpArgs(ctx, bucket, bound, opts.MCPServers)
		opts.ExtraArgs = append(opts.ExtraArgs, args...)
	}
	if !hasArg(all, "--strict-mcp-config") {
		opts.ExtraArgs = append(opts.ExtraArgs, "--strict-mcp-config")
	}
	addDisallowed(opts, guard.AgentConfigDeny())
	addDisallowed(opts, guard.SensitiveReadDeny())
	addDisallowed(opts, guard.DangerDeny())
	return cleanup
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// addDisallowed extends the run's --disallowedTools list (one flag: the first
// one found, in ExtraArgs or ContextArgs) or adds one.
func addDisallowed(opts *runner.Options, rules []string) {
	for _, list := range []*[]string{&opts.ExtraArgs, &opts.ContextArgs} {
		for i := 0; i+1 < len(*list); i++ {
			if (*list)[i] == "--disallowedTools" {
				args := append([]string{}, (*list)...) // the caller may share the slice
				if strings.TrimSpace(args[i+1]) == "" {
					args[i+1] = strings.Join(rules, ",")
				} else {
					args[i+1] += "," + strings.Join(rules, ",")
				}
				*list = args
				return
			}
		}
	}
	opts.ExtraArgs = append(opts.ExtraArgs, "--disallowedTools", strings.Join(rules, ","))
}

// purgeRunMCP removes connector configs a crashed core left behind, and the
// shared configs older versions kept for every run (they held connector secrets).
func (a *App) purgeRunMCP() {
	_ = os.RemoveAll(a.runMCPDir())
	for _, n := range []string{"mcp-plugins.json", "mcp-plugins-sandbox.json"} {
		_ = os.Remove(filepath.Join(a.Cfg.DataDir, n))
	}
}
