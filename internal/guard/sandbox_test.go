package guard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func lookOnly(found ...string) func(string) (string, error) {
	return func(b string) (string, error) {
		for _, f := range found {
			if f == b {
				return "/usr/bin/" + b, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestDetectSandbox(t *testing.T) {
	if st := DetectSandbox("linux", lookOnly("bwrap", "socat")); st.Status != SandboxOK || len(st.Missing) != 0 || st.Install != "" {
		t.Fatalf("%+v", st)
	}
	st := DetectSandbox("linux", lookOnly("bwrap", "dnf"))
	if st.Status != SandboxMissing || !reflect.DeepEqual(st.Missing, []string{"socat"}) || st.Install != "sudo dnf install -y socat" {
		t.Fatalf("%+v", st)
	}
	st = DetectSandbox("linux", lookOnly())
	if st.Install != "sudo apt-get install -y bubblewrap socat" {
		t.Fatalf("%+v", st)
	}
	if st := DetectSandbox("darwin", lookOnly("sandbox-exec")); st.Status != SandboxOK {
		t.Fatalf("%+v", st)
	}
	if st := DetectSandbox("windows", lookOnly("bwrap", "socat")); st.Status != SandboxNone {
		t.Fatalf("%+v", st)
	}
}

// SensitiveReadDeny comes from the same list CheckRunDir refuses as working folders.
func TestSensitiveReadDenySameSource(t *testing.T) {
	rules := SensitiveReadDeny()
	if len(rules) != 2*len(sensitiveHome) {
		t.Fatalf("%d rules for %d folders", len(rules), len(sensitiveHome))
	}
	for _, p := range sensitiveHome {
		if !contains(rules, "Read(~/"+p+"/**)") || !contains(rules, "Read(~/"+p+")") {
			t.Errorf("missing Read deny for %s", p)
		}
	}
	for _, want := range []string{"Read(~/.ssh/**)", "Read(~/.aws/**)", "Read(~/.gnupg/**)", "Read(~/.config/gh/**)", "Read(~/.kube/**)", "Read(~/.docker/**)", "Read(~/.netrc)", "Read(~/.claude/**)"} {
		if !contains(rules, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestDangerDeny(t *testing.T) {
	rules := DangerDeny()
	for _, want := range []string{"Bash(sudo *)", "Bash(git push --force*)", "Bash(git push -f*)", "Bash(* | sh)", "Bash(* | bash)", "PowerShell(git push --force*)", "PowerShell(* | iex)"} {
		if !contains(rules, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, r := range rules {
		if strings.Contains(r, ",") {
			t.Errorf("rule %q: commas split the --disallowedTools list", r)
		}
	}
}

func TestSandboxSettings(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".ssh", ".claude", ".config/gh"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	work, stage := filepath.Join(home, "proj"), filepath.Join(home, "stage")
	var doc struct {
		Sandbox struct {
			Enabled, FailIfUnavailable, AllowUnsandboxedCommands, AutoAllowBashIfSandboxed bool
			Filesystem                                                                     struct{ DenyWrite, DenyRead, AllowRead []string }
			Credentials                                                                    struct{ Files, EnvVars []map[string]string }
			Network                                                                        map[string]any
		}
		Permissions struct{ Allow []string }
	}
	own, plug := filepath.Join(home, ".tidebot"), filepath.Join(home, ".tidebot", "stage", "p")
	b := SandboxSettings(SandboxSpec{Home: home, WriteDirs: []string{work, stage}, ReadDirs: []string{plug}, Private: []string{own}, SecretEnv: []string{"KIMI_API_KEY", "ANTHROPIC_API_KEY", ""}})
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Sandbox
	if !s.Enabled || !s.FailIfUnavailable || s.AllowUnsandboxedCommands || !s.AutoAllowBashIfSandboxed {
		t.Fatalf("sandbox switches: %s", b)
	}
	for _, want := range []string{filepath.Join(home, ".claude"), filepath.Join(work, ".claude"), filepath.Join(work, ".mcp.json"), filepath.Join(work, "CLAUDE.md"),
		filepath.Join(work, "CLAUDE.local.md"), filepath.Join(work, "AGENTS.md"), filepath.Join(work, "GEMINI.md"), filepath.Join(work, ".codex"),
		filepath.Join(work, ".cursor"), filepath.Join(work, ".gemini"), filepath.Join(work, ".config", "opencode"), filepath.Join(stage, ".claude")} {
		if !contains(s.Filesystem.DenyWrite, want) {
			t.Errorf("denyWrite misses %s", want)
		}
	}
	// ~/.claude.json doesn't exist: not listed (a placeholder would be left there on Linux)
	if contains(s.Filesystem.DenyWrite, filepath.Join(home, ".claude.json")) {
		t.Error("missing ~/.claude.json must not be write-denied")
	}
	for _, want := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".claude"), filepath.Join(home, ".config", "gh")} {
		if !contains(s.Filesystem.DenyRead, want) {
			t.Errorf("denyRead misses %s", want)
		}
	}
	if !contains(s.Filesystem.DenyRead, own) || !reflect.DeepEqual(s.Filesystem.AllowRead, []string{work, stage, plug}) {
		t.Errorf("tidebot's folder: deny %v allow %v", s.Filesystem.DenyRead, s.Filesystem.AllowRead)
	}
	if contains(s.Filesystem.DenyRead, filepath.Join(home, ".aws")) || len(s.Credentials.Files) != len(s.Filesystem.DenyRead)-1 {
		t.Errorf("credentials: %v / %v", s.Credentials.Files, s.Filesystem.DenyRead)
	}
	var env []string
	for _, e := range s.Credentials.EnvVars {
		if e["mode"] != "deny" {
			t.Errorf("env %v", e)
		}
		env = append(env, e["name"])
	}
	if !reflect.DeepEqual(env, []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "KIMI_API_KEY"}) {
		t.Errorf("envVars %v", env)
	}
	if s.Network["strictAllowlist"] != true || len(doc.Permissions.Allow) != 0 {
		t.Errorf("web off: %s", b)
	}
	// web on: any host, no strict allowlist
	doc.Sandbox.Network, doc.Permissions.Allow = nil, nil
	if err := json.Unmarshal(SandboxSettings(SandboxSpec{Home: home, WriteDirs: []string{work}, Web: true}), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Sandbox.Network != nil || !reflect.DeepEqual(doc.Permissions.Allow, []string{"WebFetch(domain:*)"}) {
		t.Errorf("web on: %+v %+v", doc.Sandbox.Network, doc.Permissions)
	}
	// no home, no folders: still a valid, closed document
	if err := json.Unmarshal(SandboxSettings(SandboxSpec{}), &doc); err != nil || !doc.Sandbox.FailIfUnavailable {
		t.Fatal(err)
	}
}

// Windows-style paths go through filepath unchanged (the document itself is only
// written on macOS/Linux; on Windows the deny rules are the protection).
func TestSandboxSettingsWindowsStylePaths(t *testing.T) {
	work := filepath.Join("C:", "Users", "u", "proj")
	b := SandboxSettings(SandboxSpec{WriteDirs: []string{work}})
	var doc struct {
		Sandbox struct{ Filesystem struct{ DenyWrite []string } }
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if !contains(doc.Sandbox.Filesystem.DenyWrite, filepath.Join(work, ".mcp.json")) {
		t.Fatal(string(b))
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
