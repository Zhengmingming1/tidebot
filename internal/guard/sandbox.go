package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	SandboxBroken  = "broken"  // installed but can't start here (Linux: user namespaces blocked)
	SandboxNone    = "none"    // the platform has no system sandbox (native Windows)
)

// SandboxStatus says whether command runs can be sandboxed here, and if not,
// what is missing and the command that installs it (tidebot never runs sudo).
type SandboxStatus struct {
	Platform string   `json:"platform"`
	Status   string   `json:"status"`
	Missing  []string `json:"missing,omitempty"`
	Install  string   `json:"install,omitempty"`
	Error    string   `json:"error,omitempty"` // SandboxBroken: bubblewrap's own first error line
	Cause    string   `json:"cause,omitempty"` // SandboxBroken: why, when it can be told (SandboxCause*)
	Fix      string   `json:"fix,omitempty"`   // SandboxBroken: the setting that lets bwrap start (tidebot never runs sudo)
}

// Why bubblewrap is refused (SandboxStatus.Cause); "" means tidebot can't tell.
const (
	SandboxCauseContainer = "container"  // inside a container: its seccomp / AppArmor profile decides
	SandboxCauseAppArmor  = "apparmor"   // Ubuntu 24.04+: kernel.apparmor_restrict_unprivileged_userns=1
	SandboxCauseUsernsOff = "userns_off" // unprivileged user namespaces switched off (sysctl)
	// bwrap starts, but a user namespace inside it is refused (Claude Code's seccomp
	// helper needs one): Ubuntu's bwrap-userns-restrict AppArmor profile takes
	// capabilities away from bwrap's children.
	SandboxCauseAppArmorBwrap = "apparmor_bwrap"
	SandboxCauseNested        = "nested" // as above, reason unknown (old bubblewrap, a host policy)
)

// BwrapNestedPrefix starts a ProbeBwrap result when bwrap itself started but a
// second user namespace inside it was refused.
const BwrapNestedPrefix = "inside bwrap: "

// bwrapRestrictProfile is the AppArmor profile Ubuntu 24.04+ ships for bwrap
// (its children run as unpriv_bwrap, without capabilities).
const bwrapRestrictProfile = "/etc/apparmor.d/bwrap-userns-restrict"

// DiagnoseBwrap says why bubblewrap is refused here (probeErr is ProbeBwrap's
// message) and, when a kernel setting is the reason, the command that changes it
// (read is os.ReadFile, getenv os.Getenv). A container comes first: its
// /proc/sys is the host's and can't be changed from inside, so it gets no
// command. Only reads files; changes nothing.
func DiagnoseBwrap(probeErr string, read func(string) ([]byte, error), getenv func(string) string) (cause, fix string) {
	val := func(p string) string {
		b, err := read(p)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	exists := func(p string) bool { _, err := read(p); return err == nil }
	if getenv("container") != "" || exists("/.dockerenv") || exists("/run/.containerenv") {
		return SandboxCauseContainer, ""
	}
	if strings.HasPrefix(probeErr, BwrapNestedPrefix) {
		// bwrap itself started, so the sysctls below allow it; Ubuntu's profile
		// for bwrap is what strips its children. Unloading it lasts until reboot;
		// with the profile gone bwrap also needs the AppArmor sysctl off.
		if exists(bwrapRestrictProfile) && !exists("/etc/apparmor.d/disable/bwrap-userns-restrict") {
			fix = "sudo apparmor_parser -R " + bwrapRestrictProfile
			if val("/proc/sys/kernel/apparmor_restrict_unprivileged_userns") == "1" {
				fix = "sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 && " + fix
			}
			return SandboxCauseAppArmorBwrap, fix
		}
		return SandboxCauseNested, ""
	}
	if val("/proc/sys/user/max_user_namespaces") == "0" {
		return SandboxCauseUsernsOff, "sudo sysctl -w user.max_user_namespaces=10000"
	}
	if val("/proc/sys/kernel/unprivileged_userns_clone") == "0" {
		return SandboxCauseUsernsOff, "sudo sysctl -w kernel.unprivileged_userns_clone=1"
	}
	if val("/proc/sys/kernel/apparmor_restrict_unprivileged_userns") == "1" {
		return SandboxCauseAppArmor, "sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0"
	}
	return "", ""
}

// DetectSandbox checks the sandbox's dependencies on PATH (look is exec.LookPath).
func DetectSandbox(goos string, look func(string) (string, error)) SandboxStatus {
	return DetectSandboxProbe(goos, look, nil)
}

// DetectSandboxProbe is DetectSandbox plus, on Linux with both parts present,
// one start of bubblewrap (probe, e.g. ProbeBwrap; nil skips it): bwrap on PATH
// can still be refused by the kernel (Ubuntu 24.04+ AppArmor's
// apparmor_restrict_unprivileged_userns, a container's seccomp profile), and
// Claude Code then refuses every command run (failIfUnavailable).
func DetectSandboxProbe(goos string, look func(string) (string, error), probe func(bwrap string) string) SandboxStatus {
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
		} else if probe != nil {
			if p, err := look("bwrap"); err == nil {
				if msg := probe(p); msg != "" {
					st.Status, st.Error = SandboxBroken, msg
				}
			}
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

// BwrapProbeTimeout bounds ProbeBwrap; a probe that takes longer counts as "can't tell".
var BwrapProbeTimeout = 5 * time.Second

// ProbeBwrap starts bubblewrap with what Claude Code's Linux sandbox always asks
// for (its own user and network namespace) and runs true, then does it again
// with a second bubblewrap inside the first: Claude Code's seccomp helper
// creates a user namespace inside bwrap, which Ubuntu 24.04's
// bwrap-userns-restrict AppArmor profile refuses even where bwrap itself starts
// (docs/backlog.md 2026-11-28). "" means both started, or the probe couldn't
// tell (bwrap couldn't be launched, it timed out): only a refusal from
// bubblewrap itself blocks command runs. Otherwise the result is bubblewrap's
// first stderr line (at most 200 bytes), or its exit status; for the inner one
// it starts with BwrapNestedPrefix.
func ProbeBwrap(bwrap string) string {
	outer := []string{"--unshare-user", "--unshare-net", "--dev-bind", "/", "/"}
	if msg := runBwrapProbe(bwrap, append(outer, "true")...); msg != "" {
		return msg
	}
	inner := append(append(outer, bwrap, "--unshare-user", "--dev-bind", "/", "/"), "true")
	if msg := runBwrapProbe(bwrap, inner...); msg != "" {
		return BwrapNestedPrefix + msg
	}
	return ""
}

func runBwrapProbe(bwrap string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), BwrapProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bwrap, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second // don't wait on a child that kept stderr open
	err := cmd.Run()
	var exit *exec.ExitError
	if err == nil || ctx.Err() != nil || !errors.As(err, &exit) {
		return ""
	}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 200 {
				line = strings.ToValidUTF8(line[:200], "")
			}
			return line
		}
	}
	return exit.Error()
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
