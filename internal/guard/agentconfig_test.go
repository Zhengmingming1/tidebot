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
		if v := Check(call("Write", map[string]any{"file_path": p}, root), root, nil); !v.Deny || v.RuleID != "agent-config" {
			t.Errorf("Write %q: %+v", p, v)
		}
	}
	for _, p := range allow {
		if IsAgentConfigPath(p, root) {
			t.Errorf("should not flag %q", p)
		}
	}
}

func TestAgentConfigBash(t *testing.T) {
	deny := []string{"claude mcp add github -- npx x", "claude mcp add-json x '{}'", "claude plugin install foo@bar", "npx skills add owner/repo",
		"npx -y skills add x", "dws skill setup", "echo '{}' > .mcp.json", "cat x >> ~/.claude/settings.json", "tee -a CLAUDE.md < notes",
		"cp SKILL.md ~/.claude/skills/x/", "mkdir -p .claude/skills", "sed -i s/a/b/ AGENTS.md", "mv x ~/.codex/config.toml"}
	allow := []string{"cat CLAUDE.md", "ls ~/.claude", "grep -r claude .", "go test ./...", "echo hi > out.txt", "claude --version", "cp a b", "npm install skills"}
	for _, c := range deny {
		if v := Check(call("Bash", map[string]any{"command": c}, "/w"), "/w", nil); !v.Deny || v.RuleID != "agent-config" {
			t.Errorf("should deny %q (%+v)", c, v)
		}
	}
	for _, c := range allow {
		if v := Check(call("Bash", map[string]any{"command": c}, "/w"), "/w", nil); v.Deny {
			t.Errorf("should allow %q (%s)", c, v.RuleID)
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
