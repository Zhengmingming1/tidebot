package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A session continuing a run in the owner's folder: once that folder is moved
// away, or swapped for a link into ~/.ssh, a turn is refused with the folder's
// problem (the session stays usable) instead of quietly running in the session's
// scratch folder; with the folder back, the turn runs there.
func TestChatTurnOwnerFolderChecked(t *testing.T) {
	home := chatHome(t)
	a := newTestApp(t)
	ctx := context.Background()
	proj := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(proj)
	rid := originRun(t, a, runner.Options{Prompt: "first", RunMode: "edit", WorkspaceDir: proj})
	ses, err := a.CreateChatSession(ctx, webui.ChatSessionCreate{RunID: rid})
	if err != nil || ses.WorkspaceDir != real {
		t.Fatalf("session %v %+v", err, ses)
	}
	countRuns := func() (n int) {
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
		return n
	}
	refused := func(want string) {
		t.Helper()
		before := countRuns()
		_, err := a.SendChatTurn(ctx, ses.ID, "next")
		var de *guard.DirError
		if !errors.As(err, &de) || de.Problem != want {
			t.Fatalf("want folder problem %q, got %v", want, err)
		}
		if d, err := a.GetChatSession(ctx, ses.ID); err != nil || d.Status != "idle" || d.Turns != 0 {
			t.Fatalf("session after refusal %v %+v", err, d)
		}
		if countRuns() != before {
			t.Fatal("a refused turn left a run")
		}
		if _, err := os.Stat(a.chatScratchDir(ses.ID)); err == nil {
			t.Fatal("refused turn went to the scratch folder")
		}
	}

	moved := proj + "-moved"
	if err := os.Rename(proj, moved); err != nil {
		t.Fatal(err)
	}
	refused(guard.DirNotFound)

	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ssh, proj); err != nil {
		t.Fatal(err)
	}
	refused(guard.DirSensitive)

	if err := os.Remove(proj); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, proj); err != nil {
		t.Fatal(err)
	}
	st, err := a.SendChatTurn(ctx, ses.ID, "next")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, a, ses.ID, 1)
	var ws string
	_ = a.DB.QueryRow(`SELECT workspace_dir FROM runs WHERE id=?`, st.RunID).Scan(&ws)
	if ws != real {
		t.Fatalf("turn ran in %q, want %q", ws, real)
	}
}

// tidebot's own folders keep the old behaviour: a session with no folder, or
// whose task folder is gone, runs in its scratch folder.
func TestChatTurnDirOwnFolders(t *testing.T) {
	a := newTestApp(t)
	for _, saved := range []string{"", filepath.Join(a.Cfg.HomeDir, "tasks", "tsk_gone"), filepath.Join(a.Cfg.DataDir, "run-work", "run-1")} {
		dir, own, err := a.chatTurnDir("chat_x", saved)
		if err != nil || own || dir != a.chatScratchDir("chat_x") {
			t.Fatalf("%q: %q %v %v", saved, dir, own, err)
		}
	}
	task := filepath.Join(a.Cfg.HomeDir, "tasks", "tsk_1")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, own, err := a.chatTurnDir("chat_x", task); err != nil || !own || dir != task {
		t.Fatalf("task folder: %q %v %v", dir, own, err)
	}
}
