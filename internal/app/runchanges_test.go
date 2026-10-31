package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/gitsnap"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"config", "maintenance.auto", "false"}, {"config", "gc.auto", "0"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return ws
}

// An edit run in a git work tree records the files it changed (and only those),
// before the run is marked finished; the snapshot is gone afterwards and the
// repo's .git is not written.
func TestEditRunRecordsChangedFiles(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ws := gitRepo(t)
	_ = os.WriteFile(filepath.Join(ws, "before.txt"), []byte("mine\n"), 0o644) // uncommitted before the run
	head, _ := os.ReadFile(filepath.Join(ws, ".git", "index"))
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(ws, "made-by-run.txt"))
	runID, err := a.startRun(webui.RunRequest{Prompt: "fix it", Workspace: ws, Mode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	var fc, patch string
	_ = a.DB.QueryRow(`SELECT file_changes, file_patch FROM runs WHERE id=?`, runID).Scan(&fc, &patch)
	var c gitsnap.Changes
	if err := json.Unmarshal([]byte(fc), &c); err != nil {
		t.Fatalf("file_changes %q: %v", fc, err)
	}
	if c.Count != 1 || c.Files[0].Path != "made-by-run.txt" || c.Files[0].Status != "A" || c.Add != 1 {
		t.Fatalf("changes: %+v", c)
	}
	if !strings.Contains(patch, "+mock edit") {
		t.Fatalf("patch: %q", patch)
	}
	if ents, _ := os.ReadDir(a.runSnapRoot()); len(ents) != 0 {
		t.Fatalf("snapshot left: %v", ents)
	}
	if after, _ := os.ReadFile(filepath.Join(ws, ".git", "index")); string(after) != string(head) {
		t.Fatal("repo index was written")
	}
	b, _ := os.ReadFile(a.Runner.LogPath(runID))
	if !strings.Contains(string(b), "files changed: 1 (+1 -0)") {
		t.Fatalf("log: %s", b)
	}
}

// Read-only runs and folders that aren't git repos record nothing.
func TestReadRunAndPlainFolderRecordNothing(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	plain := t.TempDir()
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(plain, "x.txt"))
	for _, req := range []webui.RunRequest{
		{Prompt: "a", Workspace: plain, Mode: "edit"},
		{Prompt: "b", Workspace: gitRepo(t)},
	} {
		id, err := a.startRun(req)
		if err != nil {
			t.Fatal(err)
		}
		waitRunDone(t, a, id)
		var fc string
		_ = a.DB.QueryRow(`SELECT file_changes FROM runs WHERE id=?`, id).Scan(&fc)
		if fc != "" {
			t.Fatalf("%+v recorded %q", req, fc)
		}
	}
}

func TestPurgeRunSnaps(t *testing.T) {
	a := newTestApp(t)
	_ = os.MkdirAll(filepath.Join(a.runSnapRoot(), "run-x", "objects"), 0o700)
	a.purgeRunSnaps()
	if ents, _ := os.ReadDir(a.runSnapRoot()); len(ents) != 0 {
		t.Fatal(ents)
	}
}
