package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/provider"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// Command runs (RunMode "exec", 「可执行命令」): the safety layers under
// Bash/PowerShell. Offered in the Run dialog, tasks, re-runs and sessions.
//
//  1. The OS sandbox (macOS, Linux, WSL2) from a per-run --settings file,
//     required: Claude Code refuses to start without it, commands can't retry
//     unsandboxed (guard.SandboxSettings).
//  2. Deny rules on every platform: agent config, sensitive reads, the fixed
//     danger set (applyRunMCP adds them to every run).
//  3. Provider keys hidden from commands (sandbox credentials.envVars).
//  4. After the run, a check of the owner's agent config: a change is flagged
//     on the run and notified, never undone.
//
// Native Windows has no system sandbox: layers 2 and 4 only.

// EventAgentConfigChanged: a command run's after-run check found agent config changed.
const EventAgentConfigChanged = "agent_config_changed"

// execStopGrace: how long a stopped command run gets to clean up (runner.Options.StopGrace).
const execStopGrace = 10 * time.Second

// ErrSandboxUnavailable: the platform sandbox can't start (a dependency is missing).
var ErrSandboxUnavailable = errors.New("command sandbox unavailable")

// sandboxLook finds the sandbox's dependencies (a var for tests).
var sandboxLook = exec.LookPath

// SandboxStatus reports whether command runs can be sandboxed on this machine.
func (a *App) SandboxStatus() guard.SandboxStatus {
	return guard.DetectSandbox(runtime.GOOS, sandboxLook)
}

func (a *App) runSandboxDir() string { return filepath.Join(a.Cfg.DataDir, "run-sandbox") }

// runWorkDir holds the scratch folders of command runs started without a folder
// (removed when the run ends).
func (a *App) runWorkDir() string {
	d := filepath.Join(a.Cfg.DataDir, "run-work")
	_ = os.MkdirAll(d, 0o700)
	return d
}

// purgeRunSandbox removes sandbox settings a crashed core left behind, and the
// PreToolUse hook settings older versions wrote (hooks never ran under --bare).
func (a *App) purgeRunSandbox() {
	_ = os.RemoveAll(a.runSandboxDir())
	_ = os.RemoveAll(filepath.Join(a.Cfg.DataDir, "run-work"))
	_ = os.Remove(filepath.Join(a.Cfg.DataDir, "guard-settings.json"))
}

// applyExecSandbox prepares a command run. Call after applyGuard; the returned
// cleanup removes the run's sandbox settings.
func (a *App) applyExecSandbox(ctx context.Context, opts *runner.Options) (func(), error) {
	nop := func() {}
	st := a.SandboxStatus()
	if st.Status == guard.SandboxMissing {
		return nop, fmt.Errorf("%w: missing %s (install: %s)", ErrSandboxUnavailable, strings.Join(st.Missing, ", "), st.Install)
	}
	home, _ := os.UserHomeDir()
	dirs := runWriteDirs(opts)
	// Never let commands write tidebot's own folders (its database, settings, IPC):
	// a write folder may be inside them (a scratch folder) but never hold them.
	for _, d := range dirs {
		for _, own := range []string{a.Cfg.HomeDir, a.Cfg.DataDir, filepath.Dir(a.Cfg.IPCPath)} {
			if own != "" && guard.Within(own, d) {
				return nop, fmt.Errorf("command run: working folder %s contains tidebot's own folder", d)
			}
		}
	}
	// Owner decision (2026-11-09): command runs have network by default; skills
	// download things and call APIs. Write scope, sensitive reads, key scrub and the
	// fixed deny list still apply.
	opts.Web = true
	// Commands need Bash: no --restricted (it drops the code-running tools).
	opts.ExtraArgs = dropArg(opts.ExtraArgs, "--restricted")
	opts.ContextArgs = dropArg(opts.ContextArgs, "--restricted")
	tools := []string{"Bash"}
	if runtime.GOOS == "windows" {
		tools = append(tools, "PowerShell")
	}
	if opts.Web {
		tools = append(tools, "WebFetch", "WebSearch")
	}
	addAllowed(opts, tools)
	// See guard.SandboxSettings: env scrub breaks dontAsk and the sandbox.
	opts.EnvBase = dropEnv(opts.EnvBase, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB")
	// Commands don't get the way to tidebot's IPC socket (the sandbox hides it too).
	opts.EnvBase = dropEnv(opts.EnvBase, "TIDEBOT_IPC")
	opts.StopGrace = execStopGrace
	cleanup := nop
	if st.Status == guard.SandboxOK {
		spec := guard.SandboxSpec{Home: home, WriteDirs: dirs, ReadDirs: append(argValues(opts, "--plugin-dir"), envPathDirs(opts.EnvBase)...), Web: opts.Web,
			Private:   []string{a.Cfg.HomeDir, a.Cfg.DataDir, filepath.Dir(a.Cfg.IPCPath)},
			SecretEnv: []string{opts.Profile.AuthEnv, provider.NormalizeSecretEnv(opts.Profile.SecretEnv)}}
		if err := os.MkdirAll(a.runSandboxDir(), 0o700); err != nil {
			return nop, err
		}
		p := filepath.Join(a.runSandboxDir(), id.New("sbx")+".json")
		if err := os.WriteFile(p, guard.SandboxSettings(spec), 0o600); err != nil {
			return nop, err
		}
		opts.ExtraArgs = append(opts.ExtraArgs, "--settings", p)
		cleanup = func() { _ = os.Remove(p) }
	}
	a.watchAgentConfig(opts, home, dirs)
	return cleanup, nil
}

// watchAgentConfig fingerprints agent config right before Claude Code starts and
// compares once it exits (guard.SnapshotAgentConfig).
func (a *App) watchAgentConfig(opts *runner.Options, home string, dirs []string) {
	skip := []string{a.Cfg.HomeDir, a.Cfg.DataDir}
	var before guard.ConfigSnapshot
	prevAt, prevAfter := opts.AtStart, opts.AfterExit
	opts.AtStart = func(ctx context.Context, o *runner.Options) error {
		if prevAt != nil {
			if err := prevAt(ctx, o); err != nil {
				return err
			}
		}
		before = guard.SnapshotAgentConfig(home, dirs, skip)
		return nil
	}
	taskID := opts.TaskID
	opts.AfterExit = func(ctx context.Context, runID string) string {
		msg := ""
		if prevAfter != nil {
			msg = prevAfter(ctx, runID)
		}
		if before == nil {
			return msg
		}
		changes := before.Diff(guard.SnapshotAgentConfig(home, dirs, skip))
		if len(changes) == 0 {
			return msg
		}
		a.recordAgentConfigChanges(ctx, runID, taskID, changes)
		note := fmt.Sprintf("agent config changed: %d file(s), see the run page (not undone)", len(changes))
		if msg != "" {
			return msg + "; " + note
		}
		return note
	}
}

// recordAgentConfigChanges flags the run, logs it and notifies the owner.
func (a *App) recordAgentConfigChanges(ctx context.Context, runID, taskID string, changes []guard.ConfigChange) {
	b, _ := json.Marshal(changes)
	wctx, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if _, err := a.DB.ExecContext(wctx, `UPDATE runs SET agent_config_changes=? WHERE id=?`, string(b), runID); err != nil {
		a.Log.Warn("agent config check: save", "run_id", runID, "error", err)
	}
	if _, err := a.DB.ExecContext(wctx, `INSERT INTO operation_log(at,entity_type,entity_id,action,actor,detail_json) VALUES(?,'run',?,'agent_config_changed','system',?)`,
		time.Now().UTC().Format(time.RFC3339), runID, string(b)); err != nil {
		a.Log.Warn("agent config check: log", "run_id", runID, "error", err)
	}
	a.Log.Warn("agent config changed during a command run", "run_id", runID, "files", len(changes))
	if a.Events != nil {
		a.Events.Emit(Event{Kind: EventAgentConfigChanged, TaskID: taskID, TaskName: a.taskName(taskID), RunID: runID, Detail: changeSummary(changes)})
	}
}

// changeSummary: the first paths (home shown as ~), then how many more.
func changeSummary(changes []guard.ConfigChange) string {
	home, _ := os.UserHomeDir()
	var parts []string
	for i, c := range changes {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d", len(changes)-3))
			break
		}
		p := c.Path
		if home != "" && guard.Within(p, home) {
			if rel, err := filepath.Rel(home, p); err == nil {
				p = "~" + string(filepath.Separator) + rel
			}
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// runWriteDirs: the run's working folder and its --add-dir folders.
func runWriteDirs(opts *runner.Options) []string {
	var out []string
	if opts.WorkspaceDir != "" {
		out = append(out, opts.WorkspaceDir)
	}
	return append(out, argValues(opts, "--add-dir")...)
}

// envPathDirs: the PATH entries of the run's environment (capability commands
// tidebot manages live in its data folder and must stay runnable).
func envPathDirs(env []string) []string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], "PATH=") {
			return filepath.SplitList(strings.TrimPrefix(env[i], "PATH="))
		}
	}
	return nil
}

// argValues: the values of every flag in the run's arguments.
func argValues(opts *runner.Options, flag string) []string {
	var out []string
	for _, list := range [][]string{opts.ExtraArgs, opts.ContextArgs} {
		for i := 0; i+1 < len(list); i++ {
			if list[i] == flag {
				out = append(out, list[i+1])
			}
		}
	}
	return out
}

func dropArg(args []string, flag string) []string {
	var out []string
	for _, a := range args {
		if a != flag {
			out = append(out, a)
		}
	}
	return out
}

func dropEnv(env []string, name string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, name+"=") {
			out = append(out, e)
		}
	}
	return out
}

// addAllowed extends the run's --allowedTools list (the first one found) or adds one.
func addAllowed(opts *runner.Options, tools []string) {
	for _, list := range []*[]string{&opts.ExtraArgs, &opts.ContextArgs} {
		for i := 0; i+1 < len(*list); i++ {
			if (*list)[i] == "--allowedTools" {
				args := append([]string{}, (*list)...)
				have := map[string]bool{}
				for _, t := range strings.Split(args[i+1], ",") {
					have[strings.TrimSpace(t)] = true
				}
				for _, t := range tools {
					if !have[t] {
						if strings.TrimSpace(args[i+1]) == "" {
							args[i+1] = t
						} else {
							args[i+1] += "," + t
						}
					}
				}
				*list = args
				return
			}
		}
	}
	opts.ExtraArgs = append(opts.ExtraArgs, "--allowedTools", strings.Join(tools, ","))
}
