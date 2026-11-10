package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func sandboxDeps(t *testing.T, ok bool) {
	t.Helper()
	old := sandboxLook
	sandboxLook = func(b string) (string, error) {
		if ok || (b != "bwrap" && b != "socat" && b != "sandbox-exec") {
			return "/usr/bin/" + b, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { sandboxLook = old })
}

// A command run: no --restricted, Bash allowed, the
// required sandbox from --settings, the provider key hidden, tidebot's own
// folder unreadable, the deny rules still there.
func TestExecRunArgs(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	work := t.TempDir()
	dir := t.TempDir()
	argsFile, setFile := filepath.Join(dir, "args"), filepath.Join(dir, "settings.json")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_SETTINGS_FILE", setFile)
	if _, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: work, RunMode: "exec"}, "", 0); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argsFile)
	args := string(b)
	if strings.Contains(args, "--restricted") || !strings.Contains(args, "--settings") || !strings.Contains(args, "--allowedTools\nBash") {
		t.Fatal(args)
	}
	for _, want := range []string{"Read(~/.ssh/**)", "Bash(sudo *)", "Bash(git push -f*)", "Bash(* | bash)", "Edit(//**/CLAUDE.md)"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing deny %s", want)
		}
	}
	var doc struct {
		Sandbox struct {
			Enabled, FailIfUnavailable, AllowUnsandboxedCommands bool
			Filesystem                                           struct{ DenyWrite, DenyRead, AllowRead []string }
			Credentials                                          struct{ EnvVars []map[string]string }
			Network                                              map[string]any
		}
	}
	raw, err := os.ReadFile(setFile)
	if err != nil {
		t.Fatal("settings not passed: ", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Sandbox
	// network on (owner decision 2026-11-09): no strict allowlist
	if !s.Enabled || !s.FailIfUnavailable || s.AllowUnsandboxedCommands || s.Network["strictAllowlist"] == true {
		t.Fatalf("%s", raw)
	}
	if !contains(s.Filesystem.DenyWrite, filepath.Join(work, ".mcp.json")) || !contains(s.Filesystem.DenyRead, a.Cfg.HomeDir) || !contains(s.Filesystem.AllowRead, work) {
		t.Fatalf("%s", raw)
	}
	hasKey := false
	for _, e := range s.Credentials.EnvVars {
		hasKey = hasKey || e["name"] == "ANTHROPIC_API_KEY"
	}
	if !hasKey {
		t.Fatalf("provider key not hidden: %s", raw)
	}
	// the run's settings file is gone after the run
	if ents, _ := os.ReadDir(a.runSandboxDir()); len(ents) != 0 {
		t.Fatalf("%d sandbox files left", len(ents))
	}
}

// Where the sandbox is expected but can't start, the run is refused (Claude
// Code would refuse too: failIfUnavailable).
func TestExecRunRefusedWithoutSandbox(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no system sandbox expected here")
	}
	a := newTestApp(t)
	sandboxDeps(t, false)
	_, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: t.TempDir(), RunMode: "exec"}, "", 0)
	if !errors.Is(err, ErrSandboxUnavailable) {
		t.Fatalf("err %v", err)
	}
	if st := a.SandboxStatus(); st.Status != guard.SandboxMissing {
		t.Fatalf("%+v", st)
	}
}

// The after-run check: a command run that changed .mcp.json in its folder is
// flagged on the run and notified; the file is left as it is.
func TestExecRunAgentConfigChange(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	work := t.TempDir()
	mcp := filepath.Join(work, ".mcp.json")
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", mcp)
	ch := a.Events.Subscribe(16)
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: work, RunMode: "exec"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := a.DB.QueryRow(`SELECT agent_config_changes FROM runs WHERE id=?`, res.RunID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var changes []guard.ConfigChange
	if err := json.Unmarshal([]byte(raw), &changes); err != nil || len(changes) != 1 || changes[0].Path != mcp || changes[0].Change != "modified" {
		t.Fatalf("%q %v", raw, err)
	}
	if b, _ := os.ReadFile(mcp); string(b) != "mock edit\n" {
		t.Fatal("the change must not be undone")
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == EventAgentConfigChanged {
				if e.RunID != res.RunID || !strings.Contains(e.Detail, ".mcp.json") {
					t.Fatalf("%+v", e)
				}
				m, ok := a.EventToMessage(e)
				if !ok || !m.Urgent || m.Key != EventAgentConfigChanged {
					t.Fatalf("%+v", m)
				}
				return
			}
		case <-deadline:
			t.Fatal("no agent_config_changed event")
		}
	}
}

// A run that isn't a command run gets no check and no sandbox.
func TestEditRunNoSandbox(t *testing.T) {
	a := newTestApp(t)
	work := t.TempDir()
	mcp := filepath.Join(work, ".mcp.json")
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", mcp)
	res, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: work, RunMode: "edit"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	_ = a.DB.QueryRow(`SELECT agent_config_changes FROM runs WHERE id=?`, res.RunID).Scan(&raw)
	if raw != "" {
		t.Fatal(raw)
	}
}

// Env scrub is dropped (it breaks dontAsk and the sandbox, see guard.SandboxSettings),
// and so is the way to tidebot's IPC socket; a stop gives Claude Code time to clean up.
func TestExecSandboxEnv(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	opts := runner.Options{WorkspaceDir: t.TempDir(), ExtraArgs: []string{"--restricted", "--allowedTools", "Read"},
		EnvBase: []string{"PATH=/usr/bin", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1", "TIDEBOT_IPC=/x.sock", "LANG=C"}}
	cleanup, err := a.applyExecSandbox(context.Background(), &opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	env := strings.Join(opts.EnvBase, "\n")
	if strings.Contains(env, "SCRUB") || strings.Contains(env, "TIDEBOT_IPC") || !strings.Contains(env, "LANG=C") {
		t.Fatal(env)
	}
	if contains(opts.ExtraArgs, "--restricted") || !contains(opts.ExtraArgs, "Read,Bash,WebFetch,WebSearch") || opts.StopGrace != execStopGrace {
		t.Fatalf("%v %v", opts.ExtraArgs, opts.StopGrace)
	}
}
