package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Saving the changed-file list used to try once for 5s and drop the error, so
// the run page had no files and undo was unavailable. AfterExit must keep the
// run unfinished until a busy write has been retried and landed.
func TestEditRunRecordsChangedFilesWhenSaveBusy(t *testing.T) {
	shortWriteRetries(t)
	a := newTestApp(t)
	fakeHome(t)
	ws := gitRepo(t)
	made := filepath.Join(ws, "made-by-run.txt")
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", made)
	blockSQL(t, a, "fc", `BEFORE UPDATE ON runs WHEN NEW.file_changes<>''`)

	runID, err := a.startRun(webui.RunRequest{Prompt: "fix it", Workspace: ws, Mode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool {
		var st string
		_ = a.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&st)
		_, statErr := os.Stat(made)
		return st == "running" && statErr == nil
	})
	time.Sleep(40 * time.Millisecond)
	var st string
	if err := a.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&st); err != nil || st != "running" {
		t.Fatalf("run finished before unlock: status %q %v", st, err)
	}
	var fc string
	_ = a.DB.QueryRow(`SELECT file_changes FROM runs WHERE id=?`, runID).Scan(&fc)
	if fc != "" {
		t.Fatalf("saved before unlock: %q", fc)
	}

	unblockSQL(t, a, "fc")
	waitRunDone(t, a, runID)
	_ = a.DB.QueryRow(`SELECT file_changes FROM runs WHERE id=?`, runID).Scan(&fc)
	var c gitsnap.Changes
	if err := json.Unmarshal([]byte(fc), &c); err != nil || c.Count != 1 || c.Files[0].Path != "made-by-run.txt" {
		t.Fatalf("file_changes %q: %v %+v", fc, err, c)
	}
}

// A task that may change the owner's folder records the same file list and
// patch as the Run dialog, so the run page can show and undo them. Legacy
// ("") and exec tasks do this too; read-only tasks and the task's own folder
// in tidebot's home do not.
func TestTaskRunRecordsChangedFiles(t *testing.T) {
	a := newTestApp(t)
	sandboxDeps(t, true)
	fakeHome(t)
	ctx := context.Background()
	ws := gitRepo(t)
	made := filepath.Join(ws, "made-by-run.txt")
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", made)
	for _, mode := range []string{"", "edit", "exec"} {
		_ = os.Remove(made)
		tid := taskInFolder(t, a, "chg-"+mode, ws, mode)
		v, err := a.Tasks.Get(ctx, tid)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.runTaskTrigger(ctx, v.Task, "manual", "production"); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		var fc, patch string
		if err := a.DB.QueryRow(`SELECT file_changes, file_patch FROM runs WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, tid).Scan(&fc, &patch); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		var c gitsnap.Changes
		if err := json.Unmarshal([]byte(fc), &c); err != nil {
			t.Fatalf("mode %q file_changes %q: %v", mode, fc, err)
		}
		if c.Count != 1 || c.Files[0].Path != "made-by-run.txt" || c.Files[0].Status != "A" || c.Add != 1 {
			t.Fatalf("mode %q changes: %+v", mode, c)
		}
		if !strings.Contains(patch, "+mock edit") {
			t.Fatalf("mode %q patch: %q", mode, patch)
		}
	}
	if ents, _ := os.ReadDir(a.runSnapRoot()); len(ents) != 0 {
		t.Fatalf("snapshot left: %v", ents)
	}

	plain := t.TempDir()
	t.Setenv("TIDEBOT_MOCK_WRITE_FILE", filepath.Join(plain, "x.txt"))
	for _, tc := range []struct{ name, dir, mode string }{
		{"read", gitRepo(t), "read"},
		{"plain", plain, "edit"},
		{"own", "", "edit"},
	} {
		var tid string
		if tc.dir == "" {
			tid = mkTask(t, a, "own")
			v, _ := a.Tasks.Get(ctx, tid)
			sp := *v.Spec
			sp.RunMode = tc.mode
			if _, err := a.Tasks.UpdateSpec(ctx, tid, sp, "user", "mode"); err != nil {
				t.Fatal(err)
			}
		} else {
			tid = taskInFolder(t, a, tc.name, tc.dir, tc.mode)
		}
		v, _ := a.Tasks.Get(ctx, tid)
		if err := a.runTaskTrigger(ctx, v.Task, "manual", "production"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var fc string
		_ = a.DB.QueryRow(`SELECT file_changes FROM runs WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, tid).Scan(&fc)
		if fc != "" {
			t.Fatalf("%s recorded %q", tc.name, fc)
		}
	}
}
