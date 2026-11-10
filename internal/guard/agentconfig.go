package guard

import (
	"path/filepath"
	"strings"
)

// Agent configuration is off limits for every tidebot run: tidebot must never
// change the owner's Claude Code setup (~/.claude, a project's .claude/,
// .mcp.json, CLAUDE.md) or other agents' (Codex, Cursor, Gemini, OpenCode,
// AGENTS.md). Installing a skill or MCP server only ever happens through
// tidebot's own confirmed install, into tidebot's data folder.

// agentConfigDirs are folder names whose contents belong to an agent.
var agentConfigDirs = []string{".claude", ".codex", ".cursor", ".gemini", ".agents"}

// agentConfigFiles are file names an agent loads as instructions or config.
var agentConfigFiles = []string{".mcp.json", ".claude.json", "CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "GEMINI.md"}

// agentInstallCommands are commands that install skills, plugins or MCP servers
// into an agent's own config. Matched as command prefixes.
var agentInstallCommands = []string{
	"claude mcp add*", "claude mcp remove *", "claude plugin *",
	"npx skills *", "npx -y skills *", "dws skill *",
}

// AgentConfigDeny are the --disallowedTools rules every run gets. Edit rules
// cover every built-in tool that changes files (Write, Edit, NotebookEdit) and
// the file commands and redirections Claude Code recognizes in Bash; `//**/`
// anchors at the filesystem root, so the rule holds in the working folder, in
// --add-dir folders and in the home folder, on every drive on Windows (hooks
// can't do this: --bare runs skip them).
func AgentConfigDeny() []string {
	var out []string
	for _, d := range agentConfigDirs {
		out = append(out, "Edit(//**/"+d+"/**)")
	}
	out = append(out, "Edit(//**/.config/opencode/**)")
	for _, f := range agentConfigFiles {
		out = append(out, "Edit(//**/"+f+")")
	}
	for _, c := range agentInstallCommands {
		out = append(out, "Bash("+c+")", "PowerShell("+c+")")
	}
	return out
}

// IsAgentConfigPath reports whether p (relative to cwd if not absolute) is or
// lies inside agent configuration. Case-insensitive: macOS and Windows file
// systems usually are.
func IsAgentConfigPath(p, cwd string) bool {
	if p == "" {
		return false
	}
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	segs := strings.FieldsFunc(filepath.ToSlash(filepath.Clean(p)), func(r rune) bool { return r == '/' })
	for i, s := range segs {
		for _, d := range agentConfigDirs {
			if strings.EqualFold(s, d) && i < len(segs)-1 {
				return true
			}
		}
		if i > 0 && strings.EqualFold(segs[i-1], ".config") && strings.EqualFold(s, "opencode") && i < len(segs)-1 {
			return true
		}
	}
	if len(segs) > 0 {
		base := segs[len(segs)-1]
		for _, f := range agentConfigFiles {
			if strings.EqualFold(base, f) {
				return true
			}
		}
	}
	return false
}
