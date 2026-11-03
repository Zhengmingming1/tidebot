package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// chatHome points HOME (USERPROFILE on Windows) at an empty folder and makes the
// mock save transcripts like Claude Code: under CLAUDE_CONFIG_DIR, or ~/.claude
// when that is missing.
func chatHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TIDEBOT_MOCK_TRANSCRIPTS", "1")
	return home
}

func originRun(t *testing.T, a *App, opts runner.Options) string {
	t.Helper()
	if opts.TriggerKind == "" {
		opts.TriggerKind = "quick"
	}
	res, err := a.RunPromptEx(context.Background(), opts, "", 30*time.Second)
	if err != nil || res.RunID == "" {
		t.Fatalf("origin run %v %+v", err, res)
	}
	return res.RunID
}

func readArgs(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func waitTurn(t *testing.T, a *App, sid string, turns int) webui.ChatSessionDetail {
	t.Helper()
	var d webui.ChatSessionDetail
	waitFor(t, 20*time.Second, func() bool {
		var err error
		d, err = a.GetChatSession(context.Background(), sid)
		return err == nil && d.Status == "idle" && d.Turns == turns
	})
	return d
}

func TestChatSessionResumeOnlyInTidebotConfig(t *testing.T) {
	home := chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_ENV_FILE", envFile)

	rid := originRun(t, a, runner.Options{Prompt: "summarize the notes"})
	if m, _ := filepath.Glob(filepath.Join(a.claudeConfigDir(), "projects", "*", "*.jsonl")); len(m) != 0 {
		t.Fatalf("an ordinary run kept a transcript: %v", m)
	}
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil {
		t.Fatal(err)
	}
	if ses.Title != "summarize the notes" || ses.Status != "idle" || ses.PermissionMode != "read" || ses.WebAccess || ses.Turns != 0 {
		t.Fatalf("session %+v", ses)
	}
	if again, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid}); err != nil || again.ID != ses.ID {
		t.Fatalf("second create of the same run: %v %+v", err, again)
	}

	// Turn 1: a new Claude Code session carrying the run's record.
	st, err := a.SendChatTurn(ctx, ses.ID, "why that order?")
	if err != nil || st.Index != 1 {
		t.Fatalf("send %v %+v", err, st)
	}
	d := waitTurn(t, a, ses.ID, 1)
	args := readArgs(t, argsFile)
	csid := argAfter(args, "--session-id")
	if !isUUID(csid) || contains(args, "--resume") || contains(args, "--no-session-persistence") {
		t.Fatalf("first turn args %q", args)
	}
	for _, want := range []string{"--bare", "--strict-mcp-config", "--restricted", "--append-system-prompt"} {
		if !contains(args, want) {
			t.Errorf("first turn lacks %s: %q", want, args)
		}
	}
	deny := argAfter(args, "--disallowedTools")
	for _, w := range []string{"Edit,", "Write", "Bash", "PowerShell", "WebSearch", "WebFetch", "Edit(//**/.claude/**)", "Edit(//**/CLAUDE.md)"} {
		if !strings.Contains(deny, w) {
			t.Errorf("deny lacks %s: %s", w, deny)
		}
	}
	if strings.Count(strings.Join(args, "\n"), "--disallowedTools") != 1 {
		t.Errorf("deny rules not in one flag: %q", args)
	}
	prompt := strings.Join(args, "\n") // the mock writes one argument per line
	if !strings.Contains(prompt, "\n<previous_run") || !strings.Contains(prompt, "summarize the notes") || !strings.Contains(prompt, "why that order?\n--output-format") {
		t.Fatalf("first turn prompt %q", prompt)
	}
	if len(d.TurnList) != 2 || d.TurnList[0].RunID != rid || d.TurnList[1].Message != "why that order?" || d.TurnList[1].Status != "succeeded" {
		t.Fatalf("turns %+v", d.TurnList)
	}
	if env, _ := os.ReadFile(envFile); !strings.Contains(string(env), "CLAUDE_CONFIG_DIR="+a.claudeConfigDir()) {
		t.Fatalf("turn env %s", env)
	}
	if got := a.chatTranscripts(csid); len(got) != 1 {
		t.Fatalf("transcript not in tidebot's config dir: %v", got)
	}

	// Turn 2 resumes it, with the same read-only rules and only the message.
	if _, err := a.SendChatTurn(ctx, ses.ID, "and the second item?"); err != nil {
		t.Fatal(err)
	}
	d = waitTurn(t, a, ses.ID, 2)
	args = readArgs(t, argsFile)
	if argAfter(args, "--resume") != csid || contains(args, "--session-id") || contains(args, "--no-session-persistence") || args[1] != "and the second item?" {
		t.Fatalf("second turn args %q", args)
	}
	if !strings.Contains(argAfter(args, "--disallowedTools"), "WebFetch") || !contains(args, "--strict-mcp-config") {
		t.Fatalf("second turn lost the rules: %q", args)
	}
	if len(d.TurnList) != 3 || d.TurnList[2].Answer != "ok: and the second item?" {
		t.Fatalf("turns %+v", d.TurnList)
	}
	if list, err := a.ListChatSessions(ctx, 10); err != nil || len(list) != 1 || list[0].ID != ses.ID {
		t.Fatalf("list %v %+v", err, list)
	}

	// Nothing was written to the owner's home.
	for _, p := range []string{".claude", ".claude.json"} {
		if _, err := os.Stat(filepath.Join(home, p)); !os.IsNotExist(err) {
			t.Fatalf("%s written under HOME", p)
		}
	}

	if err := a.DeleteChatSession(ctx, ses.ID); err != nil {
		t.Fatal(err)
	}
	if got := a.chatTranscripts(csid); len(got) != 0 {
		t.Fatalf("transcript kept after delete: %v", got)
	}
	if _, err := os.Stat(filepath.Join(a.chatSessionsDir(), ses.ID)); !os.IsNotExist(err) {
		t.Fatal("session folder kept after delete")
	}
	if _, err := a.GetChatSession(ctx, ses.ID); !errors.Is(err, webui.ErrChatSessionNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=?`, rid).Scan(&n)
	if n != 1 {
		t.Fatal("deleting a session removed its runs")
	}
}

func TestChatSessionLostTranscriptStartsOver(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: originRun(t, a, runner.Options{Prompt: "p"}), WebAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "one"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	first := argAfter(readArgs(t, argsFile), "--session-id")
	if d := argAfter(readArgs(t, argsFile), "--disallowedTools"); strings.Contains(d, "WebFetch") || strings.Contains(d, "WebSearch") {
		t.Fatalf("web access on but web denied: %s", d)
	}
	os.RemoveAll(filepath.Join(a.claudeConfigDir(), "projects"))
	if _, err := a.SendChatTurn(ctx, ses.ID, "two"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 2)
	args := readArgs(t, argsFile)
	if second := argAfter(args, "--session-id"); second == "" || second == first || contains(args, "--resume") || !contains(args, `<previous_run status="succeeded">`) {
		t.Fatalf("lost transcript should start a new session with the record: %q", args)
	}
}

func TestChatSessionRefusals(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: "run_nope"}); !errors.Is(err, webui.ErrChatRunNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	rid := originRun(t, a, runner.Options{Prompt: "p"})
	a.DB.Exec(`UPDATE runs SET status='running' WHERE id=?`, rid)
	if _, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid}); !errors.Is(err, webui.ErrChatRunNotFinished) {
		t.Fatalf("running run: %v", err)
	}
	a.DB.Exec(`UPDATE runs SET status='failed' WHERE id=?`, rid)
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid, Title: "  my\n title "})
	if err != nil || ses.Title != "my title" {
		t.Fatalf("failed run: %v %+v", err, ses)
	}
	if _, err := a.SendChatTurn(ctx, ses.ID, "  "); !errors.Is(err, webui.ErrChatInvalid) {
		t.Fatalf("empty message: %v", err)
	}
	if _, err := a.SendChatTurn(ctx, "chat_nope", "x"); !errors.Is(err, webui.ErrChatSessionNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	a.DB.Exec(`UPDATE chat_sessions SET status='running' WHERE id=?`, ses.ID)
	if _, err := a.SendChatTurn(ctx, ses.ID, "x"); !errors.Is(err, webui.ErrChatSessionBusy) {
		t.Fatalf("busy: %v", err)
	}
	if err := a.DeleteChatSession(ctx, ses.ID); !errors.Is(err, webui.ErrChatSessionBusy) {
		t.Fatalf("delete while answering: %v", err)
	}
	a.recoverChatSessions(ctx)
	a.DB.Exec(`UPDATE chat_sessions SET turns=? WHERE id=?`, chatMaxTurns, ses.ID)
	if _, err := a.SendChatTurn(ctx, ses.ID, "x"); !errors.Is(err, webui.ErrChatTurnLimit) {
		t.Fatalf("turn limit (after recovery freed it): %v", err)
	}
}

func TestChatSessionWorkspace(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	own := t.TempDir()
	real, _ := filepath.EvalSymlinks(own)
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: originRun(t, a, runner.Options{Prompt: "p", WorkspaceDir: own})})
	if err != nil || ses.WorkspaceDir != real {
		t.Fatalf("owner's folder: %v %+v", err, ses)
	}
	// a quick run works in tidebot's data dir: the session gets its own scratch folder
	ses2, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: originRun(t, a, runner.Options{Prompt: "q"})})
	if err != nil || ses2.WorkspaceDir != "" {
		t.Fatalf("data dir: %v %+v", err, ses2)
	}
	if _, err := a.SendChatTurn(ctx, ses2.ID, "x"); err != nil {
		t.Fatal(err)
	}
	d := waitTurn(t, a, ses2.ID, 1)
	var ws string
	a.DB.QueryRow(`SELECT workspace_dir FROM runs WHERE id=?`, d.TurnList[1].RunID).Scan(&ws)
	if ws != a.chatScratchDir(ses2.ID) {
		t.Fatalf("turn folder %q", ws)
	}
}

func TestHasEnvLastWins(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=/a", "X=1", "CLAUDE_CONFIG_DIR=/b"}
	if !hasEnv(env, "CLAUDE_CONFIG_DIR=/b") || hasEnv(env, "CLAUDE_CONFIG_DIR=/a") || hasEnv(nil, "CLAUDE_CONFIG_DIR=/b") {
		t.Fatal("hasEnv")
	}
	if !isUUID(newClaudeSessionID()) || isUUID("../../x") {
		t.Fatal("uuid")
	}
}
