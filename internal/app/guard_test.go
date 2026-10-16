package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/policy"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

func TestGuardArgsAndConfirmations(t *testing.T) {
	a := newTestApp(t)
	a.HookExe = "/opt/tidebot/agentd"
	ctx := context.Background()
	task, err := a.EnsureTask(ctx, "g", "p", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Policy.Put(ctx, policy.Policy{TaskID: task.ID, AllowedTools: "Read, Bash(git diff *)"})
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	if _, err := a.RunPromptEx(ctx, runner.Options{Prompt: "x", TaskID: task.ID, TriggerKind: "manual"}, "", 0); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argsFile)
	args := string(b)
	if !strings.Contains(args, "--allowedTools\nRead,Bash(git diff *)") || !strings.Contains(args, "--settings") {
		t.Fatal(args)
	}
	ws := filepath.Join(a.Cfg.HomeDir, "workspaces", task.ID)
	if _, err := os.Stat(ws); err != nil {
		t.Fatal("per-task workspace not created")
	}
	settings, _ := os.ReadFile(filepath.Join(a.Cfg.DataDir, "guard-settings.json"))
	if !strings.Contains(string(settings), `agentd\" hook pretool`) {
		t.Fatal(string(settings))
	}
	// external trigger without policy tools → tightened default
	t2, _ := a.EnsureTask(ctx, "ext", "p", "", "", "")
	_, _ = a.RunPromptEx(ctx, runner.Options{Prompt: "x", TaskID: t2.ID, TriggerKind: "webhook"}, "", 0)
	b, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(b), "Bash(git status)") {
		t.Fatal("external default tools missing")
	}

	ch := a.Events.Subscribe(8)
	deny, _ := a.GuardCheck(ctx, "r1", task.ID, "Bash", "Bash:rm -rf x", "rm-rf", "")
	deny2, _ := a.GuardCheck(ctx, "r1", task.ID, "Bash", "Bash:rm -rf x", "rm-rf", "")
	if !deny || !deny2 {
		t.Fatal("must deny")
	}
	if e := <-ch; e.Kind != EventConfirmNeeded {
		t.Fatal(e.Kind)
	}
	list, _ := a.ListConfirmations(ctx, "pending")
	if len(list) != 1 {
		t.Fatalf("dedupe: %d", len(list))
	}
	if err := a.DecideConfirmation(ctx, list[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if deny, _ := a.GuardCheck(ctx, "r2", task.ID, "Bash", "Bash:rm -rf x", "rm-rf", ""); deny {
		t.Fatal("approved call should pass once")
	}
	if deny, _ := a.GuardCheck(ctx, "r3", task.ID, "Bash", "Bash:rm -rf x", "rm-rf", ""); !deny {
		t.Fatal("approval is single use")
	}
}
