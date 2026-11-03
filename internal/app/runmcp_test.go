package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func installTwoConnectors(t *testing.T, a *App) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "mcp"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(`id: mail-in
name: Mail
version: 1.0.0
contributes:
  connectors:
    - id: send
      mcp: {command: bin/mcp}
    - id: read
      mcp: {command: bin/mcp, env: {TOKEN: "${secret:token}"}}
permissions:
  secrets: [token]
`), 0o644)
	v, err := a.Plugins.Stage(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Plugins.Approve(ctx, v.ID, v.Requested); err != nil {
		t.Fatal(err)
	}
	_ = a.Plugins.SetSecret(ctx, "mail-in", "token", "tok123")
}

func TestRunMCPOnlyBoundConnectors(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	installTwoConnectors(t, a)
	stage := t.TempDir()
	os.MkdirAll(filepath.Join(stage, "skills", "mailer"), 0o700)
	os.WriteFile(filepath.Join(stage, "skills", "mailer", "SKILL.md"), []byte("---\nname: mailer\n---\nSend with mcp__mail-in__send__deliver.\n"), 0o600)
	cases := []struct {
		name  string
		opts  runner.Options
		want  []string
		avoid []string
	}{
		{"unbound run gets none", runner.Options{Prompt: "summarize the repo"}, nil, []string{"mail-in__"}},
		{"allowed tool binds one", runner.Options{Prompt: "x", ExtraArgs: []string{"--allowedTools", "Read,mcp__mail-in__read__list"}}, []string{"mail-in__read", "tok123"}, []string{"mail-in__send"}},
		{"server wildcard", runner.Options{Prompt: "x", ExtraArgs: []string{"--allowedTools", "mcp__mail-in__read__*"}}, []string{"mail-in__read"}, []string{"mail-in__send"}},
		{"plugin wildcard", runner.Options{Prompt: "x", ExtraArgs: []string{"--allowedTools", "mcp__mail-in__*"}}, []string{"mail-in__read", "mail-in__send"}, nil},
		{"skill names a tool", runner.Options{Prompt: "x", ContextArgs: []string{"--plugin-dir", stage}}, []string{"mail-in__send"}, []string{"mail-in__read", "tok123"}},
		{"prefix of another plugin does not bind", runner.Options{Prompt: "use mcp__mail__read"}, nil, []string{"mail-in__"}},
	}
	for _, c := range cases {
		opts := c.opts
		cleanup := a.applyRunMCP(ctx, &opts, "production")
		all := append(append([]string{}, opts.ExtraArgs...), opts.ContextArgs...)
		if !contains(all, "--strict-mcp-config") {
			t.Errorf("%s: no --strict-mcp-config: %q", c.name, all)
		}
		if !strings.Contains(argAfter(all, "--disallowedTools"), "Edit(//**/.claude/**)") {
			t.Errorf("%s: no agent-config deny: %q", c.name, all)
		}
		p := argAfter(all, "--mcp-config")
		if len(c.want) == 0 {
			if p != "" {
				t.Errorf("%s: unbound run got %s", c.name, p)
			}
			cleanup()
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if fi, _ := os.Stat(p); fi != nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
			t.Errorf("%s: config mode %v", c.name, fi.Mode())
		}
		for _, w := range c.want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s: missing %s in %s", c.name, w, b)
			}
		}
		for _, w := range c.avoid {
			if strings.Contains(string(b), w) {
				t.Errorf("%s: unexpected %s in %s", c.name, w, b)
			}
		}
		cleanup()
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s: config left behind", c.name)
		}
	}
}

func TestRunMCPKeepsCallerConfigAndMergesDeny(t *testing.T) {
	a := newTestApp(t)
	installTwoConnectors(t, a)
	shared := []string{"--disallowedTools", "Edit,Bash"}
	opts := runner.Options{Prompt: "mcp__mail-in__read__x", NoPluginMCP: true, ContextArgs: shared,
		ExtraArgs: []string{"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`}}
	a.applyRunMCP(context.Background(), &opts, "production")()
	all := append(append([]string{}, opts.ExtraArgs...), opts.ContextArgs...)
	if n := strings.Count(strings.Join(all, "\n"), "--strict-mcp-config"); n != 1 {
		t.Fatalf("strict flag %d times: %q", n, all)
	}
	if argAfter(all, "--mcp-config") != `{"mcpServers":{}}` || strings.Count(strings.Join(all, "\n"), "--mcp-config") != 1 {
		t.Fatalf("caller's MCP config replaced: %q", all)
	}
	d := argAfter(all, "--disallowedTools")
	if !strings.HasPrefix(d, "Edit,Bash,") || !strings.Contains(d, "Edit(//**/CLAUDE.md)") || strings.Count(strings.Join(all, "\n"), "--disallowedTools") != 1 {
		t.Fatalf("deny not merged into one flag: %q", all)
	}
	if shared[1] != "Edit,Bash" {
		t.Fatal("caller's slice was modified")
	}
}

func TestRunPromptStrictMCPAndCleanup(t *testing.T) {
	a := newTestApp(t)
	installTwoConnectors(t, a)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	os.WriteFile(filepath.Join(a.Cfg.DataDir, "mcp-plugins.json"), []byte(`{"mcpServers":{"old":{"env":{"T":"secret"}}}}`), 0o600)
	a.purgeRunMCP()
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "mcp-plugins.json")); !os.IsNotExist(err) {
		t.Fatal("legacy shared MCP config (with secrets) kept")
	}
	if _, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "use mcp__mail-in__read__list", TriggerKind: "manual",
		ExtraArgs: []string{"--permission-mode", "dontAsk", "--allowedTools", "mcp__mail-in__read__list"}}, "", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if !contains(args, "--strict-mcp-config") || argAfter(args, "--mcp-config") == "" || !strings.Contains(argAfter(args, "--disallowedTools"), "Edit(//**/.mcp.json)") {
		t.Fatalf("args %q", args)
	}
	if ents, _ := os.ReadDir(a.runMCPDir()); len(ents) != 0 {
		t.Fatalf("per-run MCP config left after the run: %v", ents)
	}
}
