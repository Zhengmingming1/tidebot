package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func runChanges(t *testing.T, a *App, runID string) (gitsnap.Changes, string) {
	t.Helper()
	var fc, patch string
	if err := a.DB.QueryRow(`SELECT file_changes, file_patch FROM runs WHERE id=?`, runID).Scan(&fc, &patch); err != nil {
		t.Fatal(err)
	}
	var c gitsnap.Changes
	if fc == "" {
		return c, patch
	}
	if err := json.Unmarshal([]byte(fc), &c); err != nil {
		t.Fatalf("file_changes %q: %v", fc, err)
	}
	return c, patch
}

func gitInitAt(t *testing.T, ws string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "maintenance.auto", "false"}, {"config", "gc.auto", "0"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// A session turn that may change the owner's git folder records only that
// turn's files on its run row, so the run page can show and undo them per
// turn. A later turn's undo must not take back an earlier turn's files.
func TestChatTurnRecordsChangedFiles(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	sandboxDeps(t, true)
	ctx := context.Background()
	ws := gitRepo(t)
	first, second := filepath.Join(ws, "turn-1.txt"), filepath.Join(ws, "turn-2.txt")

	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", first)
	rid := originRun(t, a, runner.Options{Prompt: "first", RunMode: "edit", WorkspaceDir: ws})
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil || ses.WorkspaceDir == "" {
		t.Fatalf("session %v %+v", err, ses)
	}

	for i, mode := range []string{"edit", "exec"} {
		if i > 0 {
			if _, err := a.SetChatMode(ctx, ses.ID, mode); err != nil {
				t.Fatal(err)
			}
		}
		made := filepath.Join(ws, "made-"+mode+".txt")
		_ = os.Remove(made)
		t.Setenv("TIDEBOT_MOCK_WRITE_FILE", made)
		st, err := a.SendChatTurn(ctx, ses.ID, "change files in "+mode)
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		d := waitTurn(t, a, ses.ID, st.Index)
		if d.TurnList[len(d.TurnList)-1].Status != "succeeded" {
			t.Fatalf("mode %q turn: %+v", mode, d.TurnList[len(d.TurnList)-1])
		}
		c, patch := runChanges(t, a, st.RunID)
		if c.Count != 1 || c.Files[0].Path != filepath.Base(made) || c.Files[0].Status != "A" || c.Add != 1 {
			t.Fatalf("mode %q changes: %+v", mode, c)
		}
		if !strings.Contains(patch, "+mock edit") {
			t.Fatalf("mode %q patch: %q", mode, patch)
		}
	}

	// two turns, each with its own file: undo the later turn, then the earlier
	if _, err := a.SetChatMode(ctx, ses.ID, "edit"); err != nil {
		t.Fatal(err)
	}
	beforeTurns := 0
	if d, err := a.GetChatSession(ctx, ses.ID); err == nil {
		beforeTurns = d.Turns
	}
	_ = os.Remove(first)
	_ = os.Remove(second)
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", first)
	st1, err := a.SendChatTurn(ctx, ses.ID, "write first")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, beforeTurns+1)
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", second)
	st2, err := a.SendChatTurn(ctx, ses.ID, "write second")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, beforeTurns+2)
	c1, _ := runChanges(t, a, st1.RunID)
	c2, _ := runChanges(t, a, st2.RunID)
	if c1.Count != 1 || c1.Files[0].Path != "turn-1.txt" || c2.Count != 1 || c2.Files[0].Path != "turn-2.txt" {
		t.Fatalf("per-turn changes: %+v %+v", c1, c2)
	}
	if _, err := a.undoRunChanges(ctx, st2.RunID, false, ""); err != nil {
		t.Fatalf("undo turn 2: %v", err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatal("turn 2 file still there after undo")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("undo turn 2 took back turn 1: %v", err)
	}
	if _, err := a.undoRunChanges(ctx, st1.RunID, false, ""); err != nil {
		t.Fatalf("undo turn 1: %v", err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("turn 1 file still there after undo")
	}
	if ents, _ := os.ReadDir(a.runSnapRoot()); len(ents) != 0 {
		t.Fatalf("snapshot left: %v", ents)
	}
}

// Read-only turns, folders that aren't git repos, the session scratch folder,
// and a task's own folder in tidebot's home still record nothing.
func TestChatTurnSkipsChangeTracking(t *testing.T) {
	chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()

	// read-only in a git repo
	ws := gitRepo(t)
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(ws, "x.txt"))
	rid := originRun(t, a, runner.Options{Prompt: "first", RunMode: "read", WorkspaceDir: ws})
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.SendChatTurn(ctx, ses.ID, "just look")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	if c, _ := runChanges(t, a, st.RunID); c.Count != 0 {
		t.Fatalf("read recorded %+v", c)
	}

	// edit in a folder that isn't a git repo
	plain := t.TempDir()
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(plain, "x.txt"))
	rid = originRun(t, a, runner.Options{Prompt: "first", RunMode: "edit", WorkspaceDir: plain})
	ses, err = a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil {
		t.Fatal(err)
	}
	st, err = a.SendChatTurn(ctx, ses.ID, "edit plain")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	if c, patch := runChanges(t, a, st.RunID); c.Count != 0 || patch != "" {
		t.Fatalf("plain recorded %+v %q", c, patch)
	}

	// blank session: scratch folder even if it is a git repo
	blank, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{Origin: "chat", PermissionMode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	scratch := a.chatScratchDir(blank.ID)
	gitInitAt(t, scratch)
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(scratch, "x.txt"))
	st, err = a.SendChatTurn(ctx, blank.ID, "in scratch")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, blank.ID, 1)
	if c, patch := runChanges(t, a, st.RunID); c.Count != 0 || patch != "" {
		t.Fatalf("scratch recorded %+v %q", c, patch)
	}

	// task default folder in tidebot's home
	tid := mkTask(t, a, "own-ws")
	taskWS := guard.TaskWorkspace(a.Cfg.HomeDir, tid)
	gitInitAt(t, taskWS)
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(taskWS, "x.txt"))
	rid = originRun(t, a, runner.Options{Prompt: "first", RunMode: "edit", WorkspaceDir: taskWS, TaskID: tid})
	ses, err = a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil || ses.WorkspaceDir == "" {
		t.Fatalf("task session %v %+v", err, ses)
	}
	st, err = a.SendChatTurn(ctx, ses.ID, "in task folder")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	if c, patch := runChanges(t, a, st.RunID); c.Count != 0 || patch != "" {
		t.Fatalf("task folder recorded %+v %q", c, patch)
	}
	if ents, _ := os.ReadDir(a.runSnapRoot()); len(ents) != 0 {
		t.Fatalf("snapshot left: %v", ents)
	}
}
