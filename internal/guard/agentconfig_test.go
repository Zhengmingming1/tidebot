package guard

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentConfigPaths(t *testing.T) {
	root := filepath.FromSlash("/w/proj")
	deny := []string{"CLAUDE.md", "sub/AGENTS.md", ".mcp.json", ".claude/settings.json", ".claude/skills/x/SKILL.md",
		filepath.FromSlash("/home/u/.claude/settings.json"), filepath.FromSlash("/home/u/.claude.json"), filepath.FromSlash("/home/u/.codex/config.toml"),
		filepath.FromSlash("/home/u/.cursor/mcp.json"), filepath.FromSlash("/home/u/.gemini/settings.json"), filepath.FromSlash("/home/u/.config/opencode/opencode.json"),
		"claude.md", "GEMINI.md", "CLAUDE.local.md"}
	allow := []string{"README.md", "src/claude.go", ".claude-plugin/plugin.json", "docs/claude-config/x", ".claude", "notes/AGENTS.md.bak"}
	for _, p := range deny {
		if !IsAgentConfigPath(p, root) {
			t.Errorf("should flag %q", p)
		}
	}
	for _, p := range allow {
		if IsAgentConfigPath(p, root) {
			t.Errorf("should not flag %q", p)
		}
	}
}

func TestAgentConfigDenyRules(t *testing.T) {
	rules := AgentConfigDeny()
	joined := strings.Join(rules, ",")
	for _, want := range []string{"Edit(//**/.claude/**)", "Edit(//**/.mcp.json)", "Edit(//**/CLAUDE.md)", "Edit(//**/AGENTS.md)", "Edit(//**/.codex/**)", "Bash(claude mcp add*)", "PowerShell(claude plugin *)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, r := range rules {
		if strings.HasPrefix(r, "Write(") || strings.Contains(r, ",") {
			t.Errorf("rule %q: Write(path) never matches and commas split the list", r)
		}
	}
}
