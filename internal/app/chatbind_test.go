package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func importSkill(t *testing.T, a *App, name, body string) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: test skill\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "notes.txt"), []byte("reference "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, err := a.Skills.ImportDir(context.Background(), name, d, "test")
	if err != nil {
		t.Fatal(err)
	}
	return sk.ID
}

// A session bound to a skill stages it in every later turn (read-only), next to
// the read-only "tidebot" file tools; unknown skills are refused; quick turns
// send no "run finished" notice.
func TestChatSessionBindings(t *testing.T) {
	home := chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	old := plugin.BuiltinExe
	plugin.BuiltinExe = "/opt/tidebot/agentd"
	t.Cleanup(func() { plugin.BuiltinExe = old })
	argsFile := filepath.Join(t.TempDir(), "args")
	mcpFile := filepath.Join(t.TempDir(), "mcp.json")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_MCP_FILE", mcpFile)
	sk := importSkill(t, a, "weekly-notes", "Summarize the week from notes.")

	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ses.Bindings) != 0 {
		t.Fatalf("new session bound: %+v", ses.Bindings)
	}
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Skills: []string{"skill_nope"}}); !errors.Is(err, webui.ErrChatInvalid) {
		t.Fatalf("unknown skill: %v", err)
	}
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Capabilities: []string{"cap_nope"}}); !errors.Is(err, webui.ErrChatInvalid) {
		t.Fatalf("unknown capability: %v", err)
	}
	got, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{Skills: []string{sk, sk}})
	if err != nil || len(got.Bindings) != 1 || got.Bindings[0].Name != "weekly-notes" || got.Bindings[0].Kind != "skill" {
		t.Fatalf("bind %v %+v", err, got.Bindings)
	}

	events := a.Events.Subscribe(64)
	if _, err := a.SendChatTurn(ctx, ses.ID, "what is in my notes?"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	args := readArgs(t, argsFile)
	joined := strings.Join(args, "\n")
	if !contains(args, "--plugin-dir") || !contains(args, "--append-system-prompt-file") || !strings.Contains(joined, "cannot run their commands") {
		t.Fatalf("bound skill not staged: %q", args)
	}
	deny := argAfter(args, "--disallowedTools")
	for _, w := range []string{"Edit", "Bash", "WebFetch"} {
		if !strings.Contains(deny, w) {
			t.Fatalf("binding loosened the session: %s", deny)
		}
	}
	if argAfter(args, "--allowedTools") != "mcp__tidebot" {
		t.Fatalf("allowed %q", argAfter(args, "--allowedTools"))
	}
	var cfg struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	raw, err := os.ReadFile(mcpFile)
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatalf("mcp config %v %s", err, raw)
	}
	srv, ok := cfg.Servers["tidebot"]
	if !ok || srv.Command != plugin.BuiltinExe || len(srv.Args) != 1 || srv.Args[0] != sessiontools.Arg {
		t.Fatalf("session tools server %+v", cfg.Servers)
	}
	roots := sessiontools.ParseRoots(srv.Env[sessiontools.EnvRoots])
	if len(roots) != 2 || roots[0].Dir != a.chatScratchDir(ses.ID) || !strings.HasPrefix(roots[1].Dir, a.skillStageRoot()) {
		t.Fatalf("roots %+v", roots)
	}
	// the stage is gone once the turn ended
	if left, _ := os.ReadDir(a.skillStageRoot()); len(left) != 0 {
		t.Fatalf("stage left behind: %v", left)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("~/.claude appeared: %v", err)
	}
	// the turn's run records the skill it used
	d, _ := a.GetChatSession(ctx, ses.ID)
	if rb, _ := a.Skills.RunBindings(ctx, d.TurnList[0].RunID); len(rb) != 1 || rb[0].SkillID != sk {
		t.Fatalf("run bindings %+v", rb)
	}
	for drained := false; !drained; {
		select {
		case ev := <-events:
			if ev.Kind == EventRunFinished || ev.Kind == EventRunFailed {
				t.Fatalf("quick session turn notified: %+v", ev)
			}
		default:
			drained = true
		}
	}

	// unbinding: the next turn stages nothing
	if _, err := a.SetChatBindings(ctx, ses.ID, webui.ChatBindingsInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "and now?"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 2)
	if args := readArgs(t, argsFile); contains(args, "--plugin-dir") || !contains(args, "--resume") {
		t.Fatalf("unbound turn %q", args)
	}
}

// A session continuing a run that used a skill re-attaches that skill version.
func TestChatSessionKeepsRunSkills(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	sk := importSkill(t, a, "release-notes", "Write release notes.")
	st, err := a.stageSkills(ctx, []tasksvc.SkillBinding{{SkillID: sk, Mode: "follow"}})
	if err != nil {
		t.Fatal(err)
	}
	rid := originRun(t, a, runner.Options{Prompt: "notes for v2", ContextArgs: st.Args(true, false), OnStart: a.skillStart(st, nil)})
	st.Close()
	if err := os.WriteFile(argsFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "shorter please"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	args := readArgs(t, argsFile)
	if !contains(args, "--plugin-dir") || !strings.Contains(strings.Join(args, "\n"), "cannot run their commands") {
		t.Fatalf("run's skill not re-attached: %q", args)
	}
}
