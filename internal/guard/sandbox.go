package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command runs ("exec" mode) rely on Claude Code's own OS sandbox (Seatbelt on
// macOS, bubblewrap + socat on Linux and WSL2), passed per run with --settings.
// Verified with Claude Code 2.1.301 under --bare (docs/backlog.md 2026-11-09 P1):
// sandbox and permission keys from --settings apply even though hooks don't.
// Native Windows has no system sandbox; there only the permission rules apply.

// Sandbox availability (SandboxStatus.Status).
const (
	SandboxOK      = "ok"      // the platform sandbox can start
	SandboxMissing = "missing" // a dependency is not installed (Linux: bubblewrap, socat)
	SandboxNone    = "none"    // the platform has no system sandbox (native Windows)
)

// SandboxStatus says whether command runs can be sandboxed here, and if not,
// what is missing and the command that installs it (tidebot never runs sudo).
type SandboxStatus struct {
	Platform string   `json:"platform"`
	Status   string   `json:"status"`
	Missing  []string `json:"missing,omitempty"`
	Install  string   `json:"install,omitempty"`
}

// DetectSandbox checks the sandbox's dependencies on PATH (look is exec.LookPath).
func DetectSandbox(goos string, look func(string) (string, error)) SandboxStatus {
	st := SandboxStatus{Platform: goos, Status: SandboxOK}
	switch goos {
	case "linux":
		for _, d := range []struct{ bin, pkg string }{{"bwrap", "bubblewrap"}, {"socat", "socat"}} {
			if _, err := look(d.bin); err != nil {
				st.Missing = append(st.Missing, d.pkg)
			}
		}
		if len(st.Missing) > 0 {
			st.Status = SandboxMissing
			st.Install = installHint(look, st.Missing)
		}
	case "darwin":
		if _, err := look("sandbox-exec"); err != nil {
			st.Status, st.Missing = SandboxMissing, []string{"sandbox-exec"}
		}
	default:
		st.Status = SandboxNone
	}
	return st
}

// installHint is the install command for the first package manager found.
func installHint(look func(string) (string, error), pkgs []string) string {
	p := strings.Join(pkgs, " ")
	for _, m := range []struct{ bin, cmd string }{
		{"apt-get", "sudo apt-get install -y "}, {"dnf", "sudo dnf install -y "}, {"yum", "sudo yum install -y "},
		{"pacman", "sudo pacman -S --needed "}, {"zypper", "sudo zypper install -y "}, {"apk", "sudo apk add "},
	} {
		if _, err := look(m.bin); err == nil {
			return m.cmd + p
		}
	}
	return "sudo apt-get install -y " + p
}

// SensitiveReadDeny are Read deny rules for the sensitiveHome folders (keys,
// credentials, Claude Code's own config). The sandbox covers Bash only; the
// Read tool (and Bash reads Claude Code recognizes, like cat) need these.
func SensitiveReadDeny() []string {
	var out []string
	for _, p := range sensitiveHome {
		out = append(out, "Read(~/"+p+")", "Read(~/"+p+"/**)")
	}
	return out
}

// DangerRule is a fixed, not configurable, deny for a dangerous command.
// rm -rf, git reset --hard and the like are left to the sandbox's write
// boundary (only the run's folders and temp can be written).
type DangerRule struct {
	ID         string
	Reason     string
	Bash       []string // Bash(...) patterns
	PowerShell []string // PowerShell(...) patterns (Windows)
}

// DefaultRules are the fixed danger rules every run gets as deny rules.
var DefaultRules = []DangerRule{
	{ID: "sudo", Reason: "privilege escalation", Bash: []string{"sudo", "sudo *"}},
	{ID: "git-force-push", Reason: "force push", Bash: []string{"git push --force*", "git push -f*", "git push * --force*", "git push * -f*"},
		PowerShell: []string{"git push --force*", "git push -f*", "git push * --force*", "git push * -f*"}},
	{ID: "curl-pipe-sh", Reason: "pipes a download into a shell", Bash: []string{"* | sh", "* | bash", "* | zsh", "* |sh", "* |bash"},
		PowerShell: []string{"* | iex", "* | Invoke-Expression"}},
}

// DangerDeny are DefaultRules as --disallowedTools rules.
func DangerDeny() []string {
	var out []string
	for _, r := range DefaultRules {
		for _, p := range r.Bash {
			out = append(out, "Bash("+p+")")
		}
		for _, p := range r.PowerShell {
			out = append(out, "PowerShell("+p+")")
		}
	}
	return out
}

// providerKeyEnv are the provider key variables every command run hides from
// commands, on top of the run's own (SandboxSpec.SecretEnv).
var providerKeyEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// SandboxSpec is what one command run may touch.
type SandboxSpec struct {
	Home      string   // the owner's home folder
	WriteDirs []string // the working folder and --add-dir folders: the only writable places (plus temp)
	ReadDirs  []string // more folders commands must read (e.g. a staged skill's plugin folder)
	Private   []string // folders commands may not read: tidebot's own (database, IPC socket, secrets)
	Web       bool     // commands may reach any host (otherwise none)
	SecretEnv []string // more environment variables commands must not see
}

// SandboxSettings is the run's --settings document (macOS, Linux, WSL2):
// the sandbox on and required (failIfUnavailable: Claude Code refuses to start
// rather than run commands unsandboxed), no unsandboxed retry, agent config
// write-protected in every writable folder, credentials unreadable, provider
// keys removed from commands' environment, and network closed unless Web.
//
// Not CLAUDE_CODE_SUBPROCESS_ENV_SCRUB: with Claude Code 2.1.301 it forces the
// permission mode back to default (ignoring dontAsk), left the sandboxed HOME
// writable and leaves empty placeholder files (package.json, .env, …) in the
// working folder; sandbox.credentials.envVars hides the keys without that.
func SandboxSettings(s SandboxSpec) []byte {
	var denyWrite []string
	// Writes outside WriteDirs are refused by the sandbox anyway; ~/.claude and
	// ~/.claude.json are listed as well when they exist. Not when they don't:
	// on Linux a missing write-denied path gets a 0-byte placeholder while the
	// run goes, and an empty ~/.claude.json left behind would break Claude Code.
	if s.Home != "" {
		for _, p := range []string{".claude", ".claude.json"} {
			if _, err := os.Lstat(filepath.Join(s.Home, p)); err == nil {
				denyWrite = append(denyWrite, filepath.Join(s.Home, p))
			}
		}
	}
	for _, d := range s.WriteDirs {
		if d == "" {
			continue
		}
		for _, n := range agentConfigDirs {
			denyWrite = append(denyWrite, filepath.Join(d, n))
		}
		denyWrite = append(denyWrite, filepath.Join(d, ".config", "opencode"))
		for _, n := range agentConfigFiles {
			denyWrite = append(denyWrite, filepath.Join(d, n))
		}
	}
	var denyRead []string
	var credFiles []map[string]string
	if s.Home != "" {
		for _, p := range sensitiveHome {
			full := filepath.Join(s.Home, filepath.FromSlash(p))
			if _, err := os.Lstat(full); err != nil {
				continue // nothing to hide; the home folder isn't writable, so it can't appear
			}
			denyRead = append(denyRead, full)
			credFiles = append(credFiles, map[string]string{"path": full, "mode": "deny"})
		}
	}
	// tidebot's own folders are unreadable, except the run's folders inside them
	// (the narrower allowRead wins): its database, the IPC socket (owner-only,
	// no token, so a command reaching it could drive tidebot) and secrets.
	var allowRead []string
	for _, p := range s.Private {
		if p != "" {
			denyRead = append(denyRead, p)
		}
	}
	if len(s.Private) > 0 {
		for _, d := range append(append([]string{}, s.WriteDirs...), s.ReadDirs...) {
			if d != "" {
				allowRead = append(allowRead, d)
			}
		}
	}
	envSeen := map[string]bool{}
	var envVars []map[string]string
	for _, n := range append(append([]string{}, providerKeyEnv...), s.SecretEnv...) {
		if n = strings.TrimSpace(n); n == "" || envSeen[n] {
			continue
		}
		envSeen[n] = true
		envVars = append(envVars, map[string]string{"name": n, "mode": "deny"})
	}
	sort.Slice(envVars, func(i, j int) bool { return envVars[i]["name"] < envVars[j]["name"] })
	sb := map[string]any{
		"enabled":                  true,
		"failIfUnavailable":        true,
		"allowUnsandboxedCommands": false,
		"autoAllowBashIfSandboxed": true,
		"filesystem":               map[string]any{"denyWrite": nonNil(denyWrite), "denyRead": nonNil(denyRead), "allowRead": nonNil(allowRead)},
		"credentials":              map[string]any{"files": nonNilMaps(credFiles), "envVars": nonNilMaps(envVars)},
	}
	doc := map[string]any{"sandbox": sb}
	if s.Web {
		// the sandbox takes WebFetch(domain:*) as "any host" (Claude Code ≥ 2.1.186)
		doc["permissions"] = map[string]any{"allow": []string{"WebFetch(domain:*)"}}
	} else {
		sb["network"] = map[string]any{"strictAllowlist": true}
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMaps(s []map[string]string) []map[string]string {
	if s == nil {
		return []map[string]string{}
	}
	return s
}
