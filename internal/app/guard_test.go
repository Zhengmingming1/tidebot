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

func TestGuardArgs(t *testing.T) {
	a := newTestApp(t)
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
	if !strings.Contains(args, "--allowedTools\nRead,Bash(git diff *)") || !strings.Contains(args, "--restricted") {
		t.Fatal(args)
	}
	// no hook settings any more (hooks never ran under --bare)
	if strings.Contains(args, "--settings") {
		t.Fatal("a run that isn't a command run gets no --settings: " + args)
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "guard-settings.json")); err == nil {
		t.Fatal("guard-settings.json written")
	}
	// every run: sensitive reads and the fixed danger set denied
	for _, want := range []string{"Read(~/.ssh/**)", "Read(~/.aws/**)", "Bash(sudo *)", "Bash(git push --force*)", "Bash(* | sh)", "Edit(//**/.mcp.json)"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing deny %s", want)
		}
	}
	ws := filepath.Join(a.Cfg.HomeDir, "workspaces", task.ID)
	if _, err := os.Stat(ws); err != nil {
		t.Fatal("per-task workspace not created")
	}
	// external trigger without policy tools → tightened default
	t2, _ := a.EnsureTask(ctx, "ext", "p", "", "", "")
	_, _ = a.RunPromptEx(ctx, runner.Options{Prompt: "x", TaskID: t2.ID, TriggerKind: "webhook"}, "", 0)
	b, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(b), "Bash(git status)") {
		t.Fatal("external default tools missing")
	}
}
