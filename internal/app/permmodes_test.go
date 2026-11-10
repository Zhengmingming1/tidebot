package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// 可执行命令 in the Run dialog, in the owner's folder: Bash in the sandbox (no
// --restricted, no read-only deny), the folder taken while it runs, agent
// config and sensitive reads still denied, network on.
func TestStartRunExecInFolder(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	ws := userProject(t)
	dir := t.TempDir()
	argsFile, setFile := filepath.Join(dir, "args"), filepath.Join(dir, "settings.json")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_SETTINGS_FILE", setFile)
	runID, err := a.startRun(webui.RunRequest{Prompt: "run the tests", Workspace: ws, Mode: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	args := readArgs(t, argsFile)
	joined := strings.Join(args, "\n")
	if contains(args, "--restricted") || !contains(args, "--settings") || !strings.Contains(joined, "--allowedTools\nBash") {
		t.Fatalf("%q", args)
	}
	if d := argAfter(args, "--disallowedTools"); strings.HasPrefix(d, "Edit,") || !strings.Contains(joined, "Edit(//**/CLAUDE.md)") || !strings.Contains(joined, "Read(~/.ssh/**)") || !strings.Contains(joined, "Bash(sudo *)") {
		t.Fatalf("deny rules: %q", args)
	}
	raw, _ := os.ReadFile(setFile)
	var doc struct {
		Sandbox struct {
			Network map[string]any
		}
		Permissions struct{ Allow []string }
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Sandbox.Network["strictAllowlist"] == true || !contains(doc.Permissions.Allow, "WebFetch(domain:*)") {
		t.Fatalf("command runs have network: %s", raw)
	}
	var mode, wsDir string
	_ = a.DB.QueryRow(`SELECT run_mode, workspace_dir FROM runs WHERE id=?`, runID).Scan(&mode, &wsDir)
	real, _ := filepath.EvalSymlinks(ws)
	if mode != "exec" || wsDir != real {
		t.Fatalf("row %q %q", mode, wsDir)
	}
}

// Without a folder or a skill a command run gets its own scratch folder in
// tidebot's data, gone when the run ends.
func TestStartRunExecScratch(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	runID, err := a.startRun(webui.RunRequest{Prompt: "download and sum", Mode: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	var mode, wsDir, status string
	_ = a.DB.QueryRow(`SELECT run_mode, workspace_dir, status FROM runs WHERE id=?`, runID).Scan(&mode, &wsDir, &status)
	if mode != "exec" || status != "succeeded" || !strings.HasPrefix(wsDir, filepath.Join(a.Cfg.DataDir, "run-work")) {
		t.Fatalf("row %q %q %q", mode, wsDir, status)
	}
	waitFor(t, 5e9, func() bool { _, err := os.Stat(wsDir); return os.IsNotExist(err) })
	if contains(readArgs(t, argsFile), "--restricted") {
		t.Fatal("scratch command run is restricted")
	}
}

// A command run may never write a folder that holds tidebot's own data.
func TestExecRefusesFolderHoldingTidebot(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	_, err := a.RunPromptEx(context.Background(), runner.Options{Prompt: "x", WorkspaceDir: filepath.Dir(a.Cfg.HomeDir), RunMode: "exec"}, "", 0)
	if err == nil || !strings.Contains(err.Error(), "tidebot's own folder") {
		t.Fatalf("err %v", err)
	}
}

// A task's permission level and effort come from its spec; "" (saved before
// the three levels) runs as edit always did.
func TestTaskRunModes(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	tid := mkTask(t, a, "modes")
	run := func(mode, effort string) ([]string, string) {
		t.Helper()
		v, err := a.Tasks.Get(ctx, tid)
		if err != nil {
			t.Fatal(err)
		}
		sp := *v.Spec
		sp.RunMode, sp.Effort = mode, effort
		if mode != v.Spec.RunMode || effort != v.Spec.Effort {
			if _, err := a.Tasks.UpdateSpec(ctx, tid, sp, "user", "mode"); err != nil {
				t.Fatal(err)
			}
		}
		v, _ = a.Tasks.Get(ctx, tid)
		if err := a.runTaskTrigger(ctx, v.Task, "manual", "production"); err != nil {
			t.Fatal(err)
		}
		var rm string
		_ = a.DB.QueryRow(`SELECT run_mode FROM runs WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, tid).Scan(&rm)
		return readArgs(t, argsFile), rm
	}
	args, rm := run("", "")
	if !contains(args, "--restricted") || strings.HasPrefix(argAfter(args, "--disallowedTools"), "Edit,") || contains(args, "--effort") || rm != "" {
		t.Fatalf("legacy %q %q", args, rm)
	}
	args, rm = run("read", "high")
	if !strings.Contains(strings.Join(args, "\n"), "Edit,Write") || argAfter(args, "--effort") != "high" || rm != "read" {
		t.Fatalf("read %q %q", args, rm)
	}
	args, rm = run("exec", "")
	if contains(args, "--restricted") || !contains(args, "--settings") || contains(args, "--effort") || rm != "exec" {
		t.Fatalf("exec %q %q", args, rm)
	}
	v, _ := a.Tasks.Get(ctx, tid)
	sp := *v.Spec
	sp.RunMode = "root"
	if _, err := a.Tasks.UpdateSpec(ctx, tid, sp, "user", "bad"); err == nil {
		t.Fatal("bad run mode saved")
	}
	sp.RunMode, sp.Effort = "", "max"
	if _, err := a.Tasks.UpdateSpec(ctx, tid, sp, "user", "bad"); err == nil {
		t.Fatal("bad effort saved")
	}
	var legacy tasksvc.Spec
	if err := json.Unmarshal([]byte(`{"prompt":"x","timeout_minutes":0}`), &legacy); err != nil || legacy.RunMode != "" {
		t.Fatalf("legacy spec %+v", legacy)
	}
}

// Sessions have the three levels: from a run they start at its level; the
// owner can change it between turns; a command session runs in the sandbox.
func TestChatSessionModes(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	sandboxDeps(t, true)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	rid := originRun(t, a, runner.Options{Prompt: "first", RunMode: "edit", WorkspaceDir: t.TempDir()})
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil || ses.PermissionMode != "edit" {
		t.Fatalf("from an edit run %v %+v", err, ses)
	}
	blank, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", PermissionMode: "exec"})
	if err != nil || blank.PermissionMode != "exec" {
		t.Fatalf("blank exec %v %+v", err, blank)
	}
	if _, err := a.SendChatTurn(ctx, blank.ID, "list files"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, blank.ID, 1)
	args := readArgs(t, argsFile)
	joined := strings.Join(args, "\n")
	if contains(args, "--restricted") || !contains(args, "--settings") || strings.Contains(joined, "Edit,Write") || strings.Contains(argAfter(args, "--disallowedTools"), "WebFetch") || !strings.Contains(joined, "run commands") {
		t.Fatalf("exec turn %q", args)
	}
	if s2, err := a.SetChatMode(ctx, blank.ID, "read"); err != nil || s2.PermissionMode != "read" {
		t.Fatalf("set read %v %+v", err, s2)
	}
	if _, err := a.SetChatMode(ctx, blank.ID, "root"); err == nil {
		t.Fatal("bad mode accepted")
	}
	if _, err := a.SendChatTurn(ctx, blank.ID, "again"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, blank.ID, 2)
	args = readArgs(t, argsFile)
	if !contains(args, "--restricted") || !strings.Contains(argAfter(args, "--disallowedTools"), "Edit,Write") || contains(args, "--settings") {
		t.Fatalf("read turn %q", args)
	}
}

// Preflight in 可执行命令 lists a missing sandbox as blocking.
func TestPreflightExecSandbox(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, false)
	rep, err := a.dependencyPreflight(context.Background(), webui.DependencyCheckRequest{Kind: "draft", Prompt: "hi", Mode: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.RuntimeProfile.Mode != "exec" || rep.RuntimeProfile.Restricted || len(rep.RuntimeProfile.DeniedTools) != 0 {
		t.Fatalf("profile %+v", rep.RuntimeProfile)
	}
	found := false
	for _, f := range rep.Findings {
		found = found || f.Dependency == "命令沙箱" && f.Level == "blocked"
	}
	if !found {
		t.Fatalf("findings %+v", rep.Findings)
	}
	rep, _ = a.dependencyPreflight(context.Background(), webui.DependencyCheckRequest{Kind: "draft", Prompt: "hi", Mode: "edit"})
	for _, f := range rep.Findings {
		if f.Dependency == "命令沙箱" {
			t.Fatal("sandbox finding outside 可执行命令")
		}
	}
}
