package capability

import (
	"fmt"
	"strings"
)

// ValidatePermissions prevents imported metadata from granting global execution
// or write access. Reviewable command grants must name a declared dependency;
// MCP grants must name a real component. Runtime still applies session policy.
func ValidatePermissions(l Lock) error {
	if len(l.Permissions.Tools) > 128 || len(l.Permissions.Network) > 128 {
		return fmt.Errorf("too many capability permissions")
	}
	declared := map[string]bool{}
	for _, r := range append(append([]ToolRequirement{}, l.Requires.Tools...), l.Requires.Runtimes...) {
		if executableName.MatchString(r.Name) {
			declared[r.Name] = true
		}
	}
	servers := map[string]bool{}
	for _, c := range l.Components {
		if c.Type == "mcp" && !c.Disabled {
			servers[c.Name] = true
		}
	}
	for _, grant := range l.Permissions.Tools {
		switch grant {
		case "Read", "Glob", "Grep", "LS":
			continue
		}
		if strings.HasPrefix(grant, "Bash(") && strings.HasSuffix(grant, ":*)") {
			name := strings.TrimSuffix(strings.TrimPrefix(grant, "Bash("), ":*)")
			switch strings.ToLower(name) {
			case "bash", "sh", "zsh", "fish", "cmd", "powershell", "pwsh", "env", "sudo", "su", "eval", "exec":
				return fmt.Errorf("shell-wide capability permission is forbidden")
			}
			if declared[name] && executableName.MatchString(name) {
				continue
			}
		}
		if strings.HasPrefix(grant, "mcp__") && strings.HasSuffix(grant, "__*") {
			name := strings.TrimSuffix(strings.TrimPrefix(grant, "mcp__"), "__*")
			if servers[name] && !strings.ContainsAny(name, "*(), \t\r\n") {
				continue
			}
		}
		return fmt.Errorf("unsupported or undeclared capability tool permission %q", grant)
	}
	for _, domain := range l.Permissions.Network {
		if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "/:@ \t\r\n\x00") || domain == "*" {
			return fmt.Errorf("invalid capability network permission")
		}
	}
	return nil
}
